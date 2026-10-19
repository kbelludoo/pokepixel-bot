package game

import (
	"time"
)

// AccountConfig modelo de configuração lido do accounts.json
type AccountConfig struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Enabled     bool            `json:"enabled"`
	BindIP      string          `json:"bindIP,omitempty"` // Suporte a múltiplos IPs secundários na VPS
	Credentials GameCredentials `json:"credentials"`
	Settings    GameSettings    `json:"settings"`
}

type GameCredentials struct {
	Email         string `json:"email"`
	Password      string `json:"password"`
	Token         string `json:"token,omitempty"`         // Bearer token direto (JWT)
	HandoffCode   string `json:"handoffCode,omitempty"`   // Código UUID temporário (?handoff=...)
	SessionCookie string `json:"sessionCookie,omitempty"` // Cookie de sessão se aplicável
}

type GameSettings struct {
	HuntTarget       string `json:"huntTarget"`       // ID da zona ou "auto" para cálculo dinâmico da melhor rota
	AutoHunt         bool   `json:"autoHunt"`         // Caçada automática via WebSocket
	AutoHeal         bool   `json:"autoHeal"`         // Cura e revive automático
	HPThreshold      int    `json:"hpThreshold"`      // Porcentagem de HP para cura (padrão 40%)
	AutoCapture      bool   `json:"autoCapture"`      // Captura automática de Pokémon normais
	AutoCaptureShiny bool   `json:"autoCaptureShiny"` // Captura automática de Pokémon Shiny
	AutoSell         bool   `json:"autoSell"`         // Venda automática de itens excedentes no NPC
	AutoExtract      bool   `json:"autoExtract"`      // Extração automática de material genético
	AutoDaily        bool   `json:"autoDaily"`        // Resgate automático de login diário, missões, jornada e stamina
	AutoProfessions  bool   `json:"autoProfessions"`  // Coleta automática de mineração, fazenda e pesca
	AutoBreeding     bool   `json:"autoBreeding"`     // Verificação e eclosão de ovos
	BestiaryFocus    bool   `json:"bestiaryFocus"`    // Foco prioritário em completar marcos do Bestiário
	StealthMode      bool   `json:"stealthMode"`      // Modo stealth: mascara nicks reais no dashboard
}

// ----------------------------------------------------------------------------
// API Endpoints - Structs de Requisição e Resposta (PokePixel / PokeIdle)
// ----------------------------------------------------------------------------

type AuthLoginRequest struct {
	Login        string `json:"login"`
	Password     string `json:"password"`
	CaptchaToken string `json:"captcha_token"`
}

type AuthLoginResponse struct {
	AccessToken      string   `json:"access_token"`
	RefreshToken     string   `json:"refresh_token,omitempty"`
	ClientSigningKey string   `json:"client_signing_key,omitempty"`
	ExpiresIn        int64    `json:"expires_in"`
	Error            *APIError `json:"error,omitempty"`
}

type APIError struct {
	Code         string `json:"code"`
	Message      string `json:"message"`
	RetryAfterMs int64  `json:"retry_after_ms,omitempty"`
}

type SecurityConfigResponse struct {
	TurnstileEnabled bool   `json:"turnstile_enabled"`
	TurnstileSiteKey string `json:"turnstile_site_key"`
}

type TrainerMeResponse struct {
	Trainer Trainer `json:"trainer"`
}

type Trainer struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Level     int    `json:"level"`
	Exp       int64  `json:"exp"`
	Gold      int64  `json:"gold"`
	Diamonds  int64  `json:"diamonds"`
	Vocation  string `json:"vocation,omitempty"`
	Gender    string `json:"gender,omitempty"`
	SkinID    string `json:"skin_id,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

type TeamResponse struct {
	LeaderID             string       `json:"leader_id"`
	Members              []TeamMember `json:"members"`
	LeaderSwitchReadyAt  string       `json:"leader_switch_ready_at,omitempty"`
	ServerTime           string       `json:"server_time,omitempty"`
}

type TeamMember struct {
	CreatureID string `json:"creature_id"`
	Slot       int    `json:"slot"`
	IsLeader   bool   `json:"is_leader"`
}

type Creature struct {
	ID         string    `json:"id"`
	SpeciesID  string    `json:"species_id"`
	Name       string    `json:"name"`
	Level      int       `json:"level"`
	HP         int       `json:"hp"`
	MaxHP      int       `json:"max_hp"`
	Exp        int64     `json:"exp"`
	NextExp    int64     `json:"next_exp"`
	Elements   []string  `json:"elements"`
	Moves      []string  `json:"moves,omitempty"`
	IsLeader   bool      `json:"is_leader"`
	IsShiny    bool      `json:"is_shiny"`
	Quality    string    `json:"quality"`
	Location   string    `json:"location"` // "team" ou "box"
}

type InventoryItem struct {
	ID          string `json:"id"`
	ItemID      string `json:"item_id"`
	Name        string `json:"name"`
	Type        string `json:"type"` // "potion", "capsule", "revive", "etc"
	Qty         int64  `json:"qty"`
	IconIndex   int    `json:"icon_index"`
	Multiplier  float64 `json:"catch_multiplier,omitempty"`
}

type Zone struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	World            string          `json:"world"`
	WorldName        string          `json:"world_name,omitempty"`
	RecommendedLevel int             `json:"recommended_level"`
	MinLevel         int             `json:"min_level"`
	MaxLevel         int             `json:"max_level"`
	ThemeElement     string          `json:"theme_element"`
	Elements         []string        `json:"elements"`
	EncounterMode    string          `json:"encounter_mode,omitempty"`
	Encounters       []ZoneEncounter `json:"encounters"`
	Drops            []ZoneDrop      `json:"drops,omitempty"`
	MapID            int             `json:"map_id,omitempty"`
}

type ZoneEncounter struct {
	SpeciesID string  `json:"species_id"`
	MinLevel  int     `json:"min_level"`
	MaxLevel  int     `json:"max_level"`
	Level     int     `json:"level,omitempty"`
	Weight    float64 `json:"weight,omitempty"`
}

type ZoneDrop struct {
	ItemID string  `json:"item_id"`
	Chance float64 `json:"chance"`
}

type PrepareHuntResponse struct {
	Allocation HuntAllocation `json:"allocation"`
}

type HuntAllocation struct {
	ZoneID    string `json:"zone_id"`
	ZoneName  string `json:"zone_name,omitempty"`
	MapID     int    `json:"map_id"`
	SpeciesID string `json:"species_id"`
}

type HuntAnalyzerResponse struct {
	Kills           int     `json:"kills"`
	ExpPerHour      float64 `json:"exp_per_hour"`
	GoldGained      int64   `json:"gold_gained"`
	DurationSeconds int     `json:"duration_seconds"`
}

type StaminaResponse struct {
	Stamina    int `json:"stamina"`
	MaxStamina int `json:"max_stamina"`
}

type WorkforceResponse struct {
	Slots []WorkforceSlot `json:"slots"`
}

type WorkforceSlot struct {
	Index         int    `json:"index"`
	CreatureID    string `json:"creature_id"`
	RewardsReady  bool   `json:"rewards_ready"`
	RemainingSecs int    `json:"remaining_seconds"`
}

type FishingStateResponse struct {
	IsFishing    bool  `json:"is_fishing"`
	FishingLevel int   `json:"fishing_level"`
	AutoCast     bool  `json:"auto_cast"`
}

type BreedingResponse struct {
	Eggs []BreedingEgg `json:"eggs"`
}

type BreedingEgg struct {
	ID        string    `json:"id"`
	SpeciesID string    `json:"species_id"`
	ReadyAt   time.Time `json:"ready_at"`
	IsReady   bool      `json:"is_ready"`
}

// ----------------------------------------------------------------------------
// WebSocket Messages
// ----------------------------------------------------------------------------

type WSOutboundMessage struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type WSInboundMessage struct {
	Type string `json:"type"`
	Data any    `json:"data"`
	Seq  int64  `json:"seq,omitempty"`
	TS   int64  `json:"ts,omitempty"`
}

type WSPingPayload struct {
	Nonce      string `json:"nonce,omitempty"`
	ClientTime int64  `json:"client_time"`
	Foreground bool   `json:"foreground"`
}

type WSHuntEnterPayload struct {
	MapID  int    `json:"map_id"`
	ZoneID string `json:"zone_id"`
}

// ----------------------------------------------------------------------------
// Bestiary & Species Goals
// ----------------------------------------------------------------------------

type BestiaryResponse struct {
	Summary BestiarySummary `json:"summary"`
	Tiers   []int           `json:"tiers"`
	Data    []BestiaryEntry `json:"data"`
}

type BestiarySummary struct {
	CompletedSpecies     int  `json:"completed_species"`
	TotalSpecies         int  `json:"total_species"`
	CompletedTiers       int  `json:"completed_tiers"`
	SaleBonusBasisPoints int  `json:"sale_bonus_basis_points"`
	Enabled              bool `json:"enabled"`
}

type BestiaryEntry struct {
	DexID                    int            `json:"dex_id"`
	SpeciesID                string         `json:"species_id"`
	SpeciesName              string         `json:"species_name"`
	KillCount                int            `json:"kill_count"`
	Elements                 []string       `json:"elements"`
	Rarity                   string         `json:"rarity"`
	UnlockedBonusBasisPoints int            `json:"unlocked_bonus_basis_points"`
	NextKillThreshold        int            `json:"next_kill_threshold"`
	Tiers                    []BestiaryTier `json:"tiers"`
}

type BestiaryTier struct {
	KillThreshold    int  `json:"kill_threshold"`
	BonusBasisPoints int  `json:"bonus_basis_points"`
	Completed        bool `json:"completed"`
}

type SpeciesGoal struct {
	SpeciesID    string            `json:"species_id"`
	SpeciesName  string            `json:"species_name"`
	KillCount    int               `json:"kill_count"`
	CaptureCount int               `json:"capture_count"`
	KillTiers    []SpeciesGoalTier `json:"kill_tiers"`
	CaptureTiers []SpeciesGoalTier `json:"capture_tiers"`
}

type SpeciesGoalTier struct {
	Threshold int   `json:"threshold"`
	Claimed   bool  `json:"claimed"`
	CanClaim  bool  `json:"can_claim"`
	Rewards   []any `json:"rewards"`
}

