package game

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"bot-template-go/pkg/engine"
)

const (
	DefaultAPIBaseURL     = "https://pokepixel.nietore.com/api/v1"
	DefaultClientVersion  = "2026.07.26-sync-guard-v1"
	DefaultRequestTimeout = 15 * time.Second
)

type GameClient struct {
	baseURL          string
	httpClient       *http.Client
	transport        *http.Transport
	accessToken      string
	signingKey       string
	clientVersion    string
	bindIP           string
	cb               *engine.CircuitBreaker
	mu               sync.RWMutex
}

func NewGameClient(bindIP string, cb *engine.CircuitBreaker) *GameClient {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	if bindIP != "" {
		ip := net.ParseIP(bindIP)
		if ip != nil {
			transport.DialContext = (&net.Dialer{
				LocalAddr: &net.TCPAddr{IP: ip},
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext
			log.Printf("[GameClient] Bind configurado para IP local: %s", bindIP)
		}
	}

	return &GameClient{
		baseURL:       DefaultAPIBaseURL,
		httpClient:    &http.Client{Transport: transport, Timeout: DefaultRequestTimeout},
		transport:     transport,
		clientVersion: DefaultClientVersion,
		bindIP:        bindIP,
		cb:            cb,
	}
}

func (c *GameClient) SetToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.accessToken = token
}

func (c *GameClient) GetToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.accessToken
}

func (c *GameClient) SetSigningKey(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.signingKey = key
}

// GenerateUUID gera um UUIDv4 compatível para Idempotency-Key
func GenerateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC4122
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%12x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// signRequest assina a requisição usando HMAC-SHA256 conforme o ApiClient.js oficial
func (c *GameClient) signRequest(method, path string, timestamp int64, bodyText string) (string, error) {
	c.mu.RLock()
	key := c.signingKey
	c.mu.RUnlock()

	if key == "" {
		return "", nil
	}

	// Hash SHA-256 do corpo
	h := sha256.New()
	h.Write([]byte(bodyText))
	digest := hex.EncodeToString(h.Sum(nil))

	// Path limpo sem query string
	pathOnly := strings.Split(path, "?")[0]
	payload := fmt.Sprintf("%s\n%s\n%d\n%s", method, pathOnly, timestamp, digest)

	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// DoRequest executa a requisição HTTP com validação do CircuitBreaker e headers padrão
func (c *GameClient) DoRequest(method, endpoint string, body any, respTarget any, idempotent bool) error {
	if c.cb != nil && !c.cb.AllowAction() {
		return fmt.Errorf("circuit breaker desarmado (cooldown restante: %v)", c.cb.RemainingCooldown())
	}

	url := c.baseURL + endpoint

	var bodyBytes []byte
	var bodyReader io.Reader
	bodyText := ""

	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("falha ao codificar body: %w", err)
		}
		bodyText = string(bodyBytes)
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("erro ao criar requisição: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PokeIdle-Client", "web")
	req.Header.Set("X-PokeIdle-Client-Version", c.clientVersion)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")

	token := c.GetToken()
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	if idempotent || method == http.MethodPost || method == http.MethodPut {
		req.Header.Set("Idempotency-Key", GenerateUUID())
	}

	// Assinatura HMAC se disponível
	c.mu.RLock()
	hasSigningKey := c.signingKey != ""
	c.mu.RUnlock()

	if hasSigningKey && token != "" {
		nowMs := time.Now().UnixMilli()
		sig, err := c.signRequest(method, endpoint, nowMs, bodyText)
		if err == nil && sig != "" {
			req.Header.Set("X-PokeIdle-Timestamp", fmt.Sprintf("%d", nowMs))
			req.Header.Set("X-PokeIdle-Signature", sig)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("requisição de rede falhou (%s %s): %w", method, endpoint, err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("falha ao ler resposta: %w", err)
	}

	// Verificação anti-ban e rate limits
	if isRejection, reason := engine.IsAntiCheatRejection(resp.StatusCode, string(respBytes)); isRejection {
		if c.cb != nil {
			c.cb.Trip(fmt.Sprintf("%s em %s (HTTP %d)", reason, endpoint, resp.StatusCode))
		}
		return fmt.Errorf("alerta de anti-cheat disparado: %s (HTTP %d)", reason, resp.StatusCode)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		if c.cb != nil {
			c.cb.Trip(fmt.Sprintf("Rate limit 429 em %s", endpoint))
		}
		return fmt.Errorf("HTTP 429 Too Many Requests em %s", endpoint)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d em %s: %s", resp.StatusCode, endpoint, string(respBytes))
	}

	// Operação bem-sucedida registra no CircuitBreaker
	if c.cb != nil {
		c.cb.RecordSuccess()
	}

	if respTarget != nil && len(respBytes) > 0 {
		if err := json.Unmarshal(respBytes, respTarget); err != nil {
			return fmt.Errorf("falha ao deserializar resposta de %s: %w", endpoint, err)
		}
	}

	return nil
}

// ----------------------------------------------------------------------------
// Métodos de Jogo (Game API Wrappers)
// ----------------------------------------------------------------------------

func (c *GameClient) Login(login, password string) (*AuthLoginResponse, error) {
	reqBody := AuthLoginRequest{
		Login:        login,
		Password:     password,
		CaptchaToken: "",
	}

	var res AuthLoginResponse
	err := c.DoRequest(http.MethodPost, "/auth/login", reqBody, &res, false)
	if err != nil {
		return nil, err
	}

	if res.AccessToken != "" {
		c.SetToken(res.AccessToken)
	}
	if res.ClientSigningKey != "" {
		c.SetSigningKey(res.ClientSigningKey)
	}

	return &res, nil
}

func (c *GameClient) ExchangeHandoff(code string) (*AuthLoginResponse, error) {
	reqBody := map[string]string{"code": code}
	var res AuthLoginResponse
	err := c.DoRequest(http.MethodPost, "/auth/handoff/exchange", reqBody, &res, false)
	if err != nil {
		return nil, err
	}

	if res.AccessToken != "" {
		c.SetToken(res.AccessToken)
	}
	if res.ClientSigningKey != "" {
		c.SetSigningKey(res.ClientSigningKey)
	}

	return &res, nil
}

func (c *GameClient) GetMe() (*Trainer, error) {
	var res TrainerMeResponse
	err := c.DoRequest(http.MethodGet, "/trainer/me", nil, &res, false)
	if err != nil {
		return nil, err
	}
	return &res.Trainer, nil
}

func (c *GameClient) GetTeam() (*TeamResponse, error) {
	var res TeamResponse
	err := c.DoRequest(http.MethodGet, "/team", nil, &res, false)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *GameClient) GetCreatures(location string) ([]Creature, error) {
	endpoint := "/creatures"
	if location != "" {
		endpoint += "?location=" + location
	}

	// O endpoint pode retornar []Creature ou { "data": []Creature }
	var raw json.RawMessage
	if err := c.DoRequest(http.MethodGet, endpoint, nil, &raw, false); err != nil {
		return nil, err
	}

	var list []Creature
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}

	var wrapper struct {
		Data      []Creature `json:"data"`
		Creatures []Creature `json:"creatures"`
	}
	if err := json.Unmarshal(raw, &wrapper); err == nil {
		if len(wrapper.Data) > 0 {
			return wrapper.Data, nil
		}
		return wrapper.Creatures, nil
	}

	return nil, nil
}

func (c *GameClient) GetInventory() ([]InventoryItem, error) {
	var raw json.RawMessage
	if err := c.DoRequest(http.MethodGet, "/inventory", nil, &raw, false); err != nil {
		return nil, err
	}

	var list []InventoryItem
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}

	var wrapper struct {
		Inventory []InventoryItem `json:"inventory"`
		Data      []InventoryItem `json:"data"`
	}
	if err := json.Unmarshal(raw, &wrapper); err == nil {
		if len(wrapper.Inventory) > 0 {
			return wrapper.Inventory, nil
		}
		return wrapper.Data, nil
	}

	return nil, nil
}

func (c *GameClient) GetZones() ([]Zone, error) {
	var raw json.RawMessage
	if err := c.DoRequest(http.MethodGet, "/zones", nil, &raw, false); err != nil {
		return nil, err
	}

	var list []Zone
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}

	var wrapper struct {
		Zones []Zone `json:"zones"`
		Data  []Zone `json:"data"`
	}
	if err := json.Unmarshal(raw, &wrapper); err == nil {
		if len(wrapper.Zones) > 0 {
			return wrapper.Zones, nil
		}
		return wrapper.Data, nil
	}

	return nil, nil
}

func (c *GameClient) PrepareHunt(zoneID string) (*HuntAllocation, error) {
	body := map[string]string{"zone_id": zoneID}
	var res PrepareHuntResponse
	err := c.DoRequest(http.MethodPost, "/hunts/prepare", body, &res, true)
	if err != nil {
		return nil, err
	}
	return &res.Allocation, nil
}

func (c *GameClient) StopHunt() error {
	return c.DoRequest(http.MethodPost, "/hunts/current/stop", map[string]any{}, nil, false)
}

func (c *GameClient) ReviveHunt(reviveItemID string) error {
	body := map[string]string{"revive_item_id": reviveItemID}
	return c.DoRequest(http.MethodPost, "/hunts/current/revive", body, nil, true)
}

func (c *GameClient) HealTeam() error {
	return c.DoRequest(http.MethodPost, "/team/heal", map[string]any{}, nil, false)
}

func (c *GameClient) UseItem(itemID, targetCreatureID string, qty int) error {
	if qty <= 0 {
		qty = 1
	}
	body := map[string]any{
		"item_id":            itemID,
		"target_creature_id": targetCreatureID,
		"quantity":           qty,
	}
	return c.DoRequest(http.MethodPost, "/inventory/use", body, nil, false)
}

func (c *GameClient) UpdateHuntSettings(autoCapture, autoPotion, autoSell, autoExtract map[string]any) error {
	payload := map[string]any{
		"auto_capture": autoCapture,
		"auto_potion":  autoPotion,
		"auto_sell":    autoSell,
		"auto_extract": autoExtract,
	}
	return c.DoRequest(http.MethodPatch, "/hunts/current/settings", payload, nil, false)
}

func (c *GameClient) GetHuntSettings() (map[string]any, error) {
	var res map[string]any
	err := c.DoRequest(http.MethodGet, "/hunts/settings", nil, &res, false)
	return res, err
}

func (c *GameClient) GetHuntAnalyzer() (*HuntAnalyzerResponse, error) {
	var res HuntAnalyzerResponse
	err := c.DoRequest(http.MethodGet, "/hunts/analyzer", nil, &res, false)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *GameClient) ResetHuntAnalyzer() error {
	return c.DoRequest(http.MethodPost, "/hunts/analyzer/reset", map[string]any{}, nil, false)
}

func (c *GameClient) GetStamina() (*StaminaResponse, error) {
	var res StaminaResponse
	err := c.DoRequest(http.MethodGet, "/stamina", nil, &res, false)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *GameClient) ResolveStamina() error {
	return c.DoRequest(http.MethodPost, "/stamina/resolve", map[string]any{}, nil, false)
}

func (c *GameClient) ClaimDailyLogin() error {
	return c.DoRequest(http.MethodPost, "/daily-login/claim", map[string]any{}, nil, true)
}

func (c *GameClient) ClaimAllQuests() error {
	return c.DoRequest(http.MethodPost, "/quests/claim-all", map[string]any{}, nil, true)
}

func (c *GameClient) ClaimAllSpeciesGoals() error {
	return c.DoRequest(http.MethodPost, "/species-goals/claim-all", map[string]any{}, nil, true)
}

func (c *GameClient) ClaimAllBattlePass() error {
	return c.DoRequest(http.MethodPost, "/battlepass/claim-all", map[string]any{}, nil, true)
}

func (c *GameClient) ClaimNewcomerJourney(day int) error {
	return c.DoRequest(http.MethodPost, "/newcomer-journey/claim", map[string]int{"day": day}, nil, true)
}

func (c *GameClient) ClaimAllMail() error {
	return c.DoRequest(http.MethodPost, "/mail/claim-all", map[string]any{}, nil, true)
}

func (c *GameClient) CollectMiningRewards(slot int) error {
	body := map[string]int{"slot_index": slot}
	return c.DoRequest(http.MethodPost, "/professions/mining/rewards/collect", body, nil, true)
}

func (c *GameClient) CollectFarmRewards(slot int) error {
	body := map[string]int{"slot_index": slot}
	return c.DoRequest(http.MethodPost, "/professions/farming/rewards/collect", body, nil, true)
}

func (c *GameClient) GetFishingState() (*FishingStateResponse, error) {
	var res FishingStateResponse
	err := c.DoRequest(http.MethodGet, "/professions/fishing/state", nil, &res, false)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *GameClient) StartFishing() error {
	return c.DoRequest(http.MethodPost, "/professions/fishing/start", map[string]any{}, nil, true)
}

func (c *GameClient) GetBreeding() (*BreedingResponse, error) {
	var res BreedingResponse
	err := c.DoRequest(http.MethodGet, "/breeding", nil, &res, false)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *GameClient) HatchBreeding(eggID string) error {
	endpoint := fmt.Sprintf("/breeding/%s/hatch", eggID)
	return c.DoRequest(http.MethodPost, endpoint, map[string]any{}, nil, true)
}

func (c *GameClient) SellShopItems(items []map[string]any) error {
	return c.DoRequest(http.MethodPost, "/shop/sell/items", map[string]any{"items": items}, nil, false)
}

func (c *GameClient) GetBestiary() (*BestiaryResponse, error) {
	var resp struct {
		Data BestiaryResponse `json:"data"`
	}
	err := c.DoRequest(http.MethodGet, "/bestiary", nil, &resp, false)
	if err == nil && (len(resp.Data.Data) > 0 || resp.Data.Summary.TotalSpecies > 0) {
		return &resp.Data, nil
	}
	var direct BestiaryResponse
	if err2 := c.DoRequest(http.MethodGet, "/bestiary", nil, &direct, false); err2 == nil {
		return &direct, nil
	}
	return nil, err
}

func (c *GameClient) GetSpeciesGoals() ([]SpeciesGoal, error) {
	var resp struct {
		Data []SpeciesGoal `json:"data"`
	}
	err := c.DoRequest(http.MethodGet, "/species-goals", nil, &resp, false)
	if err == nil && len(resp.Data) > 0 {
		return resp.Data, nil
	}
	var direct []SpeciesGoal
	if err2 := c.DoRequest(http.MethodGet, "/species-goals", nil, &direct, false); err2 == nil {
		return direct, nil
	}
	return nil, err
}



// ----------------------------------------------------------------------------
// Workforce / Mineração e Fazenda Presencial
// ----------------------------------------------------------------------------

func (c *GameClient) GetMiningWorkforce() (*WorkforceResponse, error) {
	var resp WorkforceResponse
	err := c.DoRequest(http.MethodGet, "/professions/mining/workforce", nil, &resp, false)
	return &resp, err
}

func (c *GameClient) AssignMiningWorker(creatureID string, slotIndex int) error {
	body := map[string]any{
		"creature_id": creatureID,
		"slot_index":  slotIndex,
	}
	return c.DoRequest(http.MethodPost, "/professions/mining/workers/assign", body, nil, true)
}

func (c *GameClient) GetFarmWorkforce() (*WorkforceResponse, error) {
	var resp WorkforceResponse
	err := c.DoRequest(http.MethodGet, "/professions/farming/workforce", nil, &resp, false)
	return &resp, err
}

func (c *GameClient) AssignFarmWorker(creatureID string, slotIndex int) error {
	body := map[string]any{
		"creature_id": creatureID,
		"slot_index":  slotIndex,
	}
	return c.DoRequest(http.MethodPost, "/professions/farming/workers/assign", body, nil, true)
}

// ----------------------------------------------------------------------------
// Map Quests (Mapas de Missão)
// ----------------------------------------------------------------------------

func (c *GameClient) GetMapQuests() (*MapQuestsResponse, error) {
	var resp MapQuestsResponse
	err := c.DoRequest(http.MethodGet, "/map-quests", nil, &resp, false)
	return &resp, err
}

func (c *GameClient) ForgeMapQuest() error {
	return c.DoRequest(http.MethodPost, "/map-quests/forge", map[string]any{}, nil, true)
}

func (c *GameClient) ClaimMapQuest(questID string) error {
	endpoint := fmt.Sprintf("/map-quests/%s/claim", questID)
	return c.DoRequest(http.MethodPost, endpoint, map[string]any{}, nil, true)
}

// ----------------------------------------------------------------------------
// Halloween 2026 Event
// ----------------------------------------------------------------------------

func (c *GameClient) GetHalloween() (*HalloweenResponse, error) {
	var resp HalloweenResponse
	err := c.DoRequest(http.MethodGet, "/seasonal/halloween-2026", nil, &resp, false)
	return &resp, err
}

func (c *GameClient) ClaimHalloween(target, day string) error {
	body := map[string]any{
		"target": target,
		"day":    day,
	}
	return c.DoRequest(http.MethodPost, "/seasonal/halloween-2026/claim", body, nil, true)
}

// ----------------------------------------------------------------------------
// PvP Missions
// ----------------------------------------------------------------------------

func (c *GameClient) GetPvPMissions() (*PvPMissionsResponse, error) {
	var resp PvPMissionsResponse
	err := c.DoRequest(http.MethodGet, "/pvp/missions", nil, &resp, false)
	return &resp, err
}

func (c *GameClient) ClaimPvPMission(missionID string) error {
	endpoint := fmt.Sprintf("/pvp/missions/%s/claim", missionID)
	return c.DoRequest(http.MethodPost, endpoint, map[string]any{}, nil, true)
}

// ----------------------------------------------------------------------------
// Shared Stones (Pedras Compartilhadas)
// ----------------------------------------------------------------------------

func (c *GameClient) GetSharedStoneOverview() (*SharedStonesOverview, error) {
	var resp SharedStonesOverview
	err := c.DoRequest(http.MethodGet, "/shared-stones/overview", nil, &resp, false)
	return &resp, err
}

func (c *GameClient) EngraveSharedStone(creatureID, itemID string, slotIndex int) error {
	body := map[string]any{
		"creature_id": creatureID,
		"item_id":     itemID,
		"slot_index":  slotIndex,
	}
	return c.DoRequest(http.MethodPost, "/shared-stones/engravings", body, nil, true)
}
