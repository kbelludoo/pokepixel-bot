package game

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"bot-template-go/pkg/engine"
	"bot-template-go/pkg/network"
)

type Bot struct {
	config  AccountConfig
	mu      sync.RWMutex
	status  engine.AccountStatus
	client  *GameClient
	socket  *network.SocketClient
	cb      *engine.CircuitBreaker

	// Estado do jogo
	trainer           *Trainer
	team              []Creature
	teamLeader        *Creature
	inventory         []InventoryItem
	zones             []Zone
	currentZone       *Zone
	currentAllocation *HuntAllocation

	// Métricas de sessão
	sessionStart   time.Time
	startExp       int64
	currentExp     int64
	sessionKills   int
	sessionGold    int64
	lastExpPerHour float64

	// Controle de loops e workers
	isHunting      bool
	ticketChan     chan string
	turnstileChan  chan string
}

func NewBot(cfg AccountConfig) *Bot {
	st := engine.NewStatus(cfg.ID, cfg.Name)
	if cfg.Settings.StealthMode {
		st.Name = fmt.Sprintf("Treinador #%s", cfg.ID)
	}
	st.CurrentArea = cfg.Settings.HuntTarget

	cbCfg := engine.DefaultCircuitBreakerConfig()
	cb := engine.NewCircuitBreaker(cfg.ID, cbCfg)

	client := NewGameClient(cfg.BindIP, cb)
	if cfg.Credentials.Token != "" {
		client.SetToken(cfg.Credentials.Token)
	}

	b := &Bot{
		config:        cfg,
		status:        st,
		client:        client,
		cb:            cb,
		ticketChan:    make(chan string, 1),
		turnstileChan: make(chan string, 1),
		sessionStart:  time.Now(),
	}

	// Configurar gatilhos do Circuit Breaker
	cbCfg.OnTrip = func(reason string) {
		b.addLog(fmt.Sprintf("🚨 [ANTI-CHEAT] Disjuntor DESARMADO: %s. Pausando caça e saves por segurança.", reason))
		b.mu.Lock()
		b.isHunting = false
		b.status.Status = "Proteção Anti-Ban Ativa (Cooldown)"
		b.mu.Unlock()
		_ = b.client.StopHunt()
	}

	cbCfg.OnReset = func() {
		b.addLog("🛡️ [ANTI-CHEAT] Disjuntor RESTAURADO. Operação normal restabelecida.")
	}

	cbCfg.OnHalfOpen = func() {
		b.addLog("⏱️ [ANTI-CHEAT] Cooldown expirado. Testando conexão com requisição canário segura...")
		_, _ = b.client.GetMe()
	}

	return b
}

func (b *Bot) ID() string {
	return b.config.ID
}

func (b *Bot) Name() string {
	return b.config.Name
}

func (b *Bot) Init(ctx context.Context) error {
	b.updateStatus(func(s *engine.AccountStatus) {
		s.Status = "Autenticando..."
	})
	b.addLog("Inicializando cliente PokePixel...")

	// 1. Autenticação (Login, Handoff ou Token direto)
	if b.client.GetToken() == "" {
		if b.config.Credentials.HandoffCode != "" {
			b.addLog(fmt.Sprintf("Trocando código de handoff (%s) por sessão...", b.config.Credentials.HandoffCode))
			loginRes, err := b.client.ExchangeHandoff(b.config.Credentials.HandoffCode)
			if err != nil {
				b.addLog(fmt.Sprintf("Aviso: troca de handoff falhou (%v). Tentando credenciais padrão...", err))
			} else {
				b.addLog("Token de sessão obtido com sucesso via Handoff!")
				if loginRes.ClientSigningKey != "" {
					b.addLog("Chave de assinatura HMAC-SHA256 recebida e vinculada à sessão.")
				}
			}
		}

		if b.client.GetToken() == "" {
			if b.config.Credentials.Email == "" || b.config.Credentials.Password == "" {
				return fmt.Errorf("credenciais ausentes: informe email/password, handoffCode ou token em accounts.json")
			}

			b.addLog(fmt.Sprintf("Efetuando login para %s...", b.config.Credentials.Email))
			loginRes, err := b.client.Login(b.config.Credentials.Email, b.config.Credentials.Password)
			if err != nil {
				b.updateStatus(func(s *engine.AccountStatus) {
					s.Status = "Falha no Login"
				})
				return fmt.Errorf("falha ao autenticar: %w", err)
			}
			b.addLog("Login realizado com sucesso! Token de acesso recebido.")
			if loginRes.ClientSigningKey != "" {
				b.addLog("Chave de assinatura HMAC-SHA256 recebida e vinculada à sessão.")
			}
		}
	} else {
		b.addLog("Utilizando Bearer Token pré-configurado.")
	}

	// 2. Carregar dados essenciais do jogador
	trainer, err := b.client.GetMe()
	if err != nil {
		b.addLog(fmt.Sprintf("Aviso ao buscar dados do treinador: %v", err))
	} else {
		b.trainer = trainer
		b.currentExp = trainer.Exp
		b.startExp = trainer.Exp
		b.updateStatus(func(s *engine.AccountStatus) {
			s.Level = trainer.Level
			s.Gold = trainer.Gold
			s.Vocation = "Treinador"
			if !b.config.Settings.StealthMode && trainer.Name != "" {
				s.Name = trainer.Name
			}
		})
		b.addLog(fmt.Sprintf("Treinador conectado: %s (Nível %d | Ouro: %d)", trainer.Name, trainer.Level, trainer.Gold))
	}

	// 3. Carregar equipe de Pokémon
	b.refreshTeam()

	// 4. Carregar inventário e zonas
	zones, err := b.client.GetZones()
	if err == nil && len(zones) > 0 {
		b.mu.Lock()
		b.zones = zones
		b.mu.Unlock()
		b.addLog(fmt.Sprintf("Catálogo carregado: %d zonas de caça disponíveis.", len(zones)))
	}

	// 5. Configurar automações no servidor via AutoHelper API
	_ = b.syncServerHuntSettings()

	b.updateStatus(func(s *engine.AccountStatus) {
		s.Status = "Pronto"
	})
	return nil
}

func (b *Bot) Run(ctx context.Context) error {
	token := b.client.GetToken()
	if token == "" {
		return fmt.Errorf("token de acesso ausente para iniciar WebSocket")
	}

	wsURL := fmt.Sprintf("wss://pokepixel.nietore.com/api/v1/ws?token=%s", url.QueryEscape(token))

	headers := make(http.Header)
	headers.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	headers.Set("Origin", "https://pokepixel.nietore.com")

	disconnectChan := make(chan error, 1)

	b.socket = network.NewSocketClient(
		wsURL,
		headers,
		func(data []byte) {
			b.handleWebSocketMessage(data)
		},
		func(err error) {
			b.addLog(fmt.Sprintf("WebSocket desconectado: %v", err))
			select {
			case disconnectChan <- err:
			default:
			}
		},
	)

	b.addLog("Conectando ao canal em tempo real WebSocket...")
	if err := b.socket.Connect(); err != nil {
		b.addLog(fmt.Sprintf("Falha inicial ao conectar WebSocket: %v", err))
	} else {
		b.addLog("WebSocket conectado com sucesso! Sessão autoritativa iniciada.")
	}

	// Iniciar Workers em background protegidos
	go b.runPingLoop(ctx)
	go b.runHuntLoop(ctx)
	go b.runHealthMonitorLoop(ctx)
	go b.runDailyClaimsLoop(ctx)
	go b.runProfessionsLoop(ctx)
	go b.runBreedingLoop(ctx)
	go b.runAutoSellLoop(ctx)
	go b.runMetricsLoop(ctx)

	for {
		select {
		case <-ctx.Done():
			b.addLog("Encerrando bot...")
			if b.socket != nil {
				b.socket.Close()
			}
			return ctx.Err()

		case discErr := <-disconnectChan:
			b.updateStatus(func(s *engine.AccountStatus) {
				s.Status = "Reconectando..."
			})
			b.addLog(fmt.Sprintf("Conexão interrompida (%v). Iniciando reconexão contínua com reautenticação...", discErr))

			backoff := 3 * time.Second
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(backoff):
				}

				// 1. Reautenticar para renovar token e chaves de sessão
				loginRes, err := b.client.Login(b.config.Credentials.Email, b.config.Credentials.Password)
				if err != nil {
					b.addLog(fmt.Sprintf("Reautenticação HTTP pendente (%v). Tentando novamente em %v...", err, backoff))
					if backoff < 30*time.Second {
						backoff *= 2
					}
					continue
				}

				// 2. Atualizar headers do WebSocket com o novo token
				headers := make(http.Header)
				headers.Set("Authorization", "Bearer "+loginRes.AccessToken)
				headers.Set("X-PokeIdle-Client-Version", b.client.GetClientVersion())
				headers.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
				b.socket.SetHeaders(headers)

				// 3. Reconectar socket
				if err := b.socket.Connect(); err != nil {
					b.addLog(fmt.Sprintf("Falha ao reconectar WebSocket (%v). Tentando novamente em %v...", err, backoff))
					if backoff < 30*time.Second {
						backoff *= 2
					}
					continue
				}

				b.addLog("WebSocket reconectado com sucesso após reautenticação!")
				b.resumeHuntSession()
				break
			}
		}
	}
}

// ----------------------------------------------------------------------------
// WebSocket Handler
// ----------------------------------------------------------------------------

func (b *Bot) handleWebSocketMessage(data []byte) {
	var raw struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
		Seq  int64           `json:"seq"`
		TS   int64           `json:"ts"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}

	switch raw.Type {
	case "connection.ready":
		b.addLog("⚡ [WS] Servidor autorizou conexão (connection.ready).")
		b.resumeHuntSession()

	case "connection.pong":
		// Ping respondido com sucesso

	case "combat.started":
		b.updateStatus(func(s *engine.AccountStatus) {
			s.Status = "hunting"
		})

	case "loot.received", "hunt.kill_reward":
		var loot struct {
			Gold     int64  `json:"gold"`
			Exp      int64  `json:"exp"`
			TrainerExp int64 `json:"trainer_exp"`
			Items    []any  `json:"items"`
		}
		_ = json.Unmarshal(raw.Data, &loot)

		b.mu.Lock()
		b.sessionKills++
		b.sessionGold += loot.Gold
		if loot.Exp > 0 {
			b.currentExp += loot.Exp
		}
		b.status.Kills = b.sessionKills
		b.status.Gold += loot.Gold
		currentKills := b.sessionKills
		b.mu.Unlock()

		if b.config.Settings.BestiaryFocus && currentKills%25 == 0 {
			go func() {
				_ = b.client.ClaimAllSpeciesGoals()
				b.recalculateOptimalRoute()
			}()
		}

	case "level.up", "trainer.level_up":
		var lvl struct {
			Level    int   `json:"level"`
			HP       int   `json:"hp"`
			MaxHP    int   `json:"max_hp"`
			Diamonds int64 `json:"diamonds"`
		}
		_ = json.Unmarshal(raw.Data, &lvl)

		b.updateStatus(func(s *engine.AccountStatus) {
			if lvl.Level > 0 {
				s.Level = lvl.Level
			}
			if lvl.MaxHP > 0 {
				s.MaxHP = lvl.MaxHP
				s.HP = lvl.MaxHP
			}
		})
		b.addLog(fmt.Sprintf("🎉 PARABÉNS! SUBIU PARA O NÍVEL %d!", lvl.Level))

		// Se configurado para rota automática, reavalia a melhor zona para o novo nível
		if b.config.Settings.HuntTarget == "auto" || b.config.Settings.HuntTarget == "" {
			go b.recalculateOptimalRoute()
		}

	case "combat.player_defeated":
		b.addLog("⚠️ Pokémon líder foi derrotado em combate! Ativando protocolo de recuperação...")
		go b.handleDefeat()

	case "capture.success":
		var cap struct {
			SpeciesID string `json:"species_id"`
			Name      string `json:"name"`
			IsShiny   bool   `json:"is_shiny"`
		}
		_ = json.Unmarshal(raw.Data, &cap)
		name := cap.Name
		if name == "" {
			name = cap.SpeciesID
		}
		if cap.IsShiny {
			b.addLog(fmt.Sprintf("🌟 CAPTURA SHINY INCRÍVEL: %s!", name))
		} else {
			b.addLog(fmt.Sprintf("🎯 Pokémon capturado com sucesso: %s", name))
		}

	case "hunt.stopped":
		b.mu.Lock()
		b.isHunting = false
		b.mu.Unlock()
	}
}

// ----------------------------------------------------------------------------
// Rotinas em Segundo Plano (Loops Autônomos)
// ----------------------------------------------------------------------------

func (b *Bot) runPingLoop(ctx context.Context) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if b.socket != nil && b.socket.IsConnected() {
				ping := WSOutboundMessage{
					Type: "ping",
					Data: WSPingPayload{
						ClientTime: time.Now().UnixMilli(),
						Foreground: true,
					},
				}
				_ = b.socket.SendJSON(ping)
			}
		}
	}
}

func (b *Bot) runHuntLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !b.config.Settings.AutoHunt {
				continue
			}
			if b.cb != nil && !b.cb.AllowAction() {
				continue
			}

			b.mu.RLock()
			hunting := b.isHunting
			b.mu.RUnlock()

			if !hunting && b.socket != nil && b.socket.IsConnected() {
				b.startHunting()
			}
		}
	}
}

func (b *Bot) startHunting() {
	b.mu.Lock()
	if b.isHunting {
		b.mu.Unlock()
		return
	}
	zones := b.zones
	leader := b.teamLeader
	trainerLevel := b.status.Level
	isBestiary := b.config.Settings.BestiaryFocus || b.config.Settings.HuntTarget == "bestiary"
	targetConfig := b.config.Settings.HuntTarget
	b.mu.Unlock()

	// 1. Determinar a zona
	var targetZone *Zone
	var bestiaryReason string

	if isBestiary {
		bestiary, _ := b.client.GetBestiary()
		goals, _ := b.client.GetSpeciesGoals()
		dec := FindBestiaryZone(zones, leader, trainerLevel, bestiary, goals)
		if dec != nil {
			targetZone = dec.Zone
			bestiaryReason = dec.Reason
		}
	}

	if targetZone == nil {
		if targetConfig == "auto" || targetConfig == "" || targetConfig == "bestiary" {
			targetZone = FindOptimalZone(zones, leader)
		} else {
			for i := range zones {
				if zones[i].ID == targetConfig {
					targetZone = &zones[i]
					break
				}
			}
			if targetZone == nil {
				targetZone = FindOptimalZone(zones, leader)
			}
		}
	}

	if targetZone == nil {
		b.addLog("Aviso: Nenhuma zona disponível para iniciar caçada.")
		return
	}

	if bestiaryReason != "" {
		b.addLog(fmt.Sprintf("📖 %s", bestiaryReason))
	}

	b.mu.Lock()
	b.currentZone = targetZone
	b.status.CurrentArea = targetZone.Name
	b.mu.Unlock()

	b.addLog(fmt.Sprintf("Preparando entrada na zona: %s (Nível Rec: %d)...", targetZone.Name, targetZone.RecommendedLevel))
	alloc, err := b.client.PrepareHunt(targetZone.ID)
	if err != nil {
		b.addLog(fmt.Sprintf("Erro ao preparar caçada em %s: %v", targetZone.Name, err))
		return
	}

	b.mu.Lock()
	b.currentAllocation = alloc
	b.mu.Unlock()

	// 2. Enviar entrada autoritativa via WebSocket
	enterMsg := WSOutboundMessage{
		Type: "huntsim.enter",
		Data: WSHuntEnterPayload{
			MapID:  alloc.MapID,
			ZoneID: alloc.ZoneID,
		},
	}

	if b.socket != nil {
		if err := b.socket.SendJSON(enterMsg); err != nil {
			b.addLog(fmt.Sprintf("Falha ao enviar huntsim.enter: %v", err))
			return
		}
	}

	b.mu.Lock()
	b.isHunting = true
	b.status.Status = "hunting"
	b.mu.Unlock()

	b.addLog(fmt.Sprintf("⚔️ Caçada ativa no mapa %d (%s)!", alloc.MapID, targetZone.Name))
}

func (b *Bot) resumeHuntSession() {
	if b.socket == nil || !b.socket.IsConnected() {
		return
	}
	// Resumo de caçada
	_ = b.socket.SendJSON(WSOutboundMessage{
		Type: "hunt.resume",
		Data: map[string]any{"last_seq": 0},
	})
	b.mu.RLock()
	alloc := b.currentAllocation
	b.mu.RUnlock()

	if alloc != nil {
		_ = b.socket.SendJSON(WSOutboundMessage{
			Type: "huntsim.enter",
			Data: WSHuntEnterPayload{
				MapID:  alloc.MapID,
				ZoneID: alloc.ZoneID,
			},
		})
	}
}

func (b *Bot) recalculateOptimalRoute() {
	b.mu.RLock()
	zones := b.zones
	leader := b.teamLeader
	currZone := b.currentZone
	trainerLevel := b.status.Level
	isBestiary := b.config.Settings.BestiaryFocus || b.config.Settings.HuntTarget == "bestiary"
	b.mu.RUnlock()

	var newZone *Zone
	var logMsg string

	if isBestiary {
		bestiary, _ := b.client.GetBestiary()
		goals, _ := b.client.GetSpeciesGoals()
		dec := FindBestiaryZone(zones, leader, trainerLevel, bestiary, goals)
		if dec != nil {
			newZone = dec.Zone
			logMsg = fmt.Sprintf("🔄 [Foco Bestiário] %s -> Transitando para %s (Lv. %d)...", dec.Reason, newZone.Name, newZone.RecommendedLevel)
		}
	}

	if newZone == nil {
		newZone = FindOptimalZone(zones, leader)
		if newZone != nil {
			logMsg = fmt.Sprintf("🔄 [Dynamic Route Solver] Zona ótima recalculada: %s (Lv. %d). Transitando...", newZone.Name, newZone.RecommendedLevel)
		}
	}

	if newZone == nil || (currZone != nil && currZone.ID == newZone.ID) {
		return
	}

	b.addLog(logMsg)
	_ = b.client.StopHunt()

	b.mu.Lock()
	b.isHunting = false
	b.currentZone = newZone
	b.status.CurrentArea = newZone.Name
	b.mu.Unlock()

	b.startHunting()
}

func (b *Bot) handleDefeat() {
	if !b.config.Settings.AutoHeal {
		return
	}

	b.addLog("Executando protocolo de cura e ressuscitação...")
	// Tentar reviver na caçada usando item revive
	err := b.client.ReviveHunt("revive_basic")
	if err != nil {
		// Se não tiver revive, curar o time inteiro na Enfermeira Joy (Centro Pokémon)
		healErr := b.client.HealTeam()
		if healErr != nil {
			b.addLog(fmt.Sprintf("Falha ao curar equipe: %v", healErr))
		} else {
			b.addLog("✨ Equipe curada com sucesso no Centro Pokémon!")
		}
	} else {
		b.addLog("✨ Pokémon revivido em combate!")
	}

	b.refreshTeam()
}

func (b *Bot) runHealthMonitorLoop(ctx context.Context) {
	ticker := time.NewTicker(8 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !b.config.Settings.AutoHeal {
				continue
			}

			b.mu.RLock()
			leader := b.teamLeader
			threshold := b.config.Settings.HPThreshold
			if threshold <= 0 {
				threshold = 40
			}
			b.mu.RUnlock()

			if leader != nil && leader.MaxHP > 0 {
				hpPct := (leader.HP * 100) / leader.MaxHP
				if hpPct <= 0 {
					b.handleDefeat()
				} else if hpPct < threshold {
					// Usar poção se disponível
					_ = b.client.UseItem("potion_great", leader.ID, 1)
				}
			}
		}
	}
}

func (b *Bot) runDailyClaimsLoop(ctx context.Context) {
	// Executa uma vez logo após inicializar e depois periodicamente
	time.Sleep(5 * time.Second)
	b.claimAllDailies()

	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if b.config.Settings.AutoDaily {
				b.claimAllDailies()
			}
		}
	}
}

func (b *Bot) claimAllDailies() {
	if b.cb != nil && !b.cb.AllowAction() {
		return
	}
	b.addLog("🎁 Coletando recompensas diárias, missões e stamina...")

	if err := b.client.ClaimDailyLogin(); err == nil {
		b.addLog("✓ Recompensa de Login Diário coletada.")
	}
	if err := b.client.ResolveStamina(); err == nil {
		b.addLog("✓ Farm Offline e Stamina processados.")
	}
	if err := b.client.ClaimAllQuests(); err == nil {
		b.addLog("✓ Missões concluídas resgatadas.")
	}
	if err := b.client.ClaimAllSpeciesGoals(); err == nil {
		b.addLog("✓ Metas de Espécies resgatadas.")
	}
	if err := b.client.ClaimAllBattlePass(); err == nil {
		b.addLog("✓ Recompensas do Passe de Batalha resgatadas.")
	}
	if err := b.client.ClaimAllMail(); err == nil {
		b.addLog("✓ Todas as mensagens e anexos do correio resgatados.")
	}
	for day := 1; day <= 7; day++ {
		_ = b.client.ClaimNewcomerJourney(day)
	}

	// 1. Evento Halloween 2026
	if hw, err := b.client.GetHalloween(); err == nil && hw != nil {
		goals := map[string]int{"kills": 100, "captures": 10, "costume_captures": 1}
		for _, d := range hw.Days {
			claimedMap := make(map[string]bool)
			for _, c := range d.Claimed {
				claimedMap[c] = true
			}
			if d.Kills >= goals["kills"] && !claimedMap["kills"] {
				if err := b.client.ClaimHalloween("kills", d.Day); err == nil {
					b.addLog(fmt.Sprintf("🎃 [Halloween] Recompensa de 100 abates resgatada (+50 doces, dia %s)", d.Day))
				}
			}
			if d.Captures >= goals["captures"] && !claimedMap["captures"] {
				if err := b.client.ClaimHalloween("captures", d.Day); err == nil {
					b.addLog(fmt.Sprintf("🎃 [Halloween] Recompensa de 10 capturas resgatada (+50 doces, dia %s)", d.Day))
				}
			}
			if d.CostumeCaptures >= goals["costume_captures"] && !claimedMap["costume_captures"] {
				if err := b.client.ClaimHalloween("costume_captures", d.Day); err == nil {
					b.addLog(fmt.Sprintf("🎃 [Halloween] Recompensa de fantasia resgatada (+50 doces, dia %s)", d.Day))
				}
			}
		}
	}

	// 2. Missões de PvP Diário
	if pvpRes, err := b.client.GetPvPMissions(); err == nil && pvpRes != nil {
		for _, m := range pvpRes.Missions {
			if m.Completed && !m.Claimed {
				if err := b.client.ClaimPvPMission(m.ID); err == nil {
					b.addLog(fmt.Sprintf("⚔️ [PvP] Missão diária '%s' resgatada!", m.Name))
				}
			}
		}
	}

	// 3. Map Quests (Mapas de Missão)
	if mq, err := b.client.GetMapQuests(); err == nil && mq != nil {
		for _, q := range mq.Quests {
			if q.Status == "completed" || (q.Goal > 0 && q.Progress >= q.Goal) {
				if err := b.client.ClaimMapQuest(q.ID); err == nil {
					b.addLog(fmt.Sprintf("🗺️ [Map Quest] Missão de mapa '%s' concluída e resgatada!", q.Name))
				}
			}
		}
		if mq.Fragments >= mq.FragmentsRequired && mq.Active < mq.MaxActive {
			if err := b.client.ForgeMapQuest(); err == nil {
				b.addLog("🗺️ [Map Quest] Novo mapa forjado a partir de fragmentos!")
			}
		}
	}

	// 4. Pedras Compartilhadas (Shared Stones)
	if ss, err := b.client.GetSharedStoneOverview(); err == nil && ss != nil {
		for _, it := range ss.Items {
			if it.Quantity > 0 {
				creatures, _ := b.client.GetCreatures("team")
				for _, c := range creatures {
					if c.SharedStones < 10 {
						if err := b.client.EngraveSharedStone(c.ID, it.ItemID, c.SharedStones); err == nil {
							b.addLog(fmt.Sprintf("💎 [Pedra Compartilhada] Pedra %s gravada com sucesso em %s!", it.Name, c.Name))
							break
						}
					}
				}
			}
		}
	}
}

func (b *Bot) runProfessionsLoop(ctx context.Context) {
	time.Sleep(10 * time.Second)
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !b.config.Settings.AutoProfessions {
				continue
			}
			if b.cb != nil && !b.cb.AllowAction() {
				continue
			}

			// 1. Mineração: verificar se precisa alocar trabalhador no slot 1
			mining, err := b.client.GetMiningWorkforce()
			if err == nil && mining != nil {
				if len(mining.Data.Workers) == 0 && len(mining.Data.Candidates) > 0 {
					cand := mining.Data.Candidates[0]
					if err := b.client.AssignMiningWorker(cand.ID, 1); err == nil {
						b.addLog(fmt.Sprintf("⛏️ [Profissões] %s atribuído à Mina de Pedras (Slot 1)!", cand.Name))
					}
				}
			}

			// 2. Fazenda: verificar se precisa alocar trabalhador no slot 1
			farming, err := b.client.GetFarmWorkforce()
			if err == nil && farming != nil {
				if len(farming.Data.Workers) == 0 && len(farming.Data.Candidates) > 0 {
					cand := farming.Data.Candidates[0]
					if err := b.client.AssignFarmWorker(cand.ID, 1); err == nil {
						b.addLog(fmt.Sprintf("🌾 [Profissões] %s atribuído à Fazenda (Slot 1)!", cand.Name))
					}
				}
			}

			// Coletar recompensas dos slots 1, 2, 3
			for slot := 1; slot <= 3; slot++ {
				_ = b.client.CollectMiningRewards(slot)
				_ = b.client.CollectFarmRewards(slot)
			}

			// Verificar pesca automática
			fishing, err := b.client.GetFishingState()
			if err == nil && !fishing.IsFishing {
				_ = b.client.StartFishing()
			}
		}
	}
}

func (b *Bot) runBreedingLoop(ctx context.Context) {
	time.Sleep(15 * time.Second)
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !b.config.Settings.AutoBreeding {
				continue
			}
			if b.cb != nil && !b.cb.AllowAction() {
				continue
			}

			res, err := b.client.GetBreeding()
			if err == nil && res != nil {
				for _, egg := range res.Eggs {
					if egg.IsReady || time.Now().After(egg.ReadyAt) {
						b.addLog(fmt.Sprintf("🥚 Chocando ovo de Pokémon (Espécie: %s)...", egg.SpeciesID))
						_ = b.client.HatchBreeding(egg.ID)
					}
				}
			}
		}
	}
}

func (b *Bot) runAutoSellLoop(ctx context.Context) {
	time.Sleep(20 * time.Second)
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !b.config.Settings.AutoSell {
				continue
			}
			if b.cb != nil && !b.cb.AllowAction() {
				continue
			}

			items, err := b.client.GetInventory()
			if err == nil && len(items) > 0 {
				var toSell []map[string]any
				for _, item := range items {
					// Vende itens de drop comuns em excesso
					if strings.HasPrefix(item.ItemID, "prof_stone_fragment") && item.Qty > 100 {
						toSell = append(toSell, map[string]any{"item_id": item.ItemID, "qty": item.Qty - 50})
					}
				}
				if len(toSell) > 0 {
					_ = b.client.SellShopItems(toSell)
					b.addLog(fmt.Sprintf("💰 [AutoSell] %d tipos de itens vendidos no NPC.", len(toSell)))
				}
			}
		}
	}
}

func (b *Bot) runMetricsLoop(ctx context.Context) {
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.mu.Lock()
			duration := int(time.Since(b.sessionStart).Seconds())
			b.status.DurationSec = duration
			b.status.UpdatedAt = time.Now().UnixMilli()

			// Calcular Exp/Hora em tempo real
			expGained := b.currentExp - b.startExp
			if duration > 10 && expGained > 0 {
				b.status.ExpPerHour = (float64(expGained) / float64(duration)) * 3600.0
			}

			// Informações extras para o Dashboard
			if b.status.Extra == nil {
				b.status.Extra = make(map[string]any)
			}
			if b.cb != nil {
				b.status.Extra["circuitState"] = b.cb.StateString()
				b.status.Extra["cooldownSec"] = int(b.cb.RemainingCooldown().Seconds())
			}
			if b.teamLeader != nil {
				b.status.Extra["leaderName"] = b.teamLeader.Name
				b.status.Extra["leaderLevel"] = b.teamLeader.Level
			}
			b.status.Extra["bestiaryFocus"] = b.config.Settings.BestiaryFocus
			b.mu.Unlock()
		}
	}
}

// ----------------------------------------------------------------------------
// Helpers Internos
// ----------------------------------------------------------------------------

func (b *Bot) refreshTeam() {
	creatures, err := b.client.GetCreatures("team")
	if err != nil || len(creatures) == 0 {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.team = creatures
	for i := range creatures {
		if creatures[i].IsLeader || b.teamLeader == nil {
			b.teamLeader = &creatures[i]
		}
	}

	if b.teamLeader != nil {
		b.status.HP = b.teamLeader.HP
		b.status.MaxHP = b.teamLeader.MaxHP
		if b.teamLeader.Level > 0 {
			b.status.Level = b.teamLeader.Level
		}
	}
}

func (b *Bot) syncServerHuntSettings() error {
	autoCapture := map[string]any{
		"enabled":                 b.config.Settings.AutoCapture || b.config.Settings.AutoCaptureShiny,
		"common_enabled":          b.config.Settings.AutoCapture,
		"shiny_enabled":           b.config.Settings.AutoCaptureShiny,
		"common_capsule_item_id":  "capsule_basic",
		"shiny_capsule_item_id":   "capsule_ultra",
		"min_quality":             "common",
	}

	autoPotion := map[string]any{
		"enabled":        b.config.Settings.AutoHeal,
		"hp_threshold":   b.config.Settings.HPThreshold,
		"potion_item_id": "potion_great",
		"auto_revive":    true,
		"revive_item_id": "revive_basic",
	}

	autoSell := map[string]any{
		"enabled":   b.config.Settings.AutoSell,
		"qualities": []string{"weak", "common"},
	}

	autoExtract := map[string]any{
		"enabled":   b.config.Settings.AutoExtract,
		"qualities": []string{"weak", "common"},
	}

	return b.client.UpdateHuntSettings(autoCapture, autoPotion, autoSell, autoExtract)
}

func (b *Bot) addLog(msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	entry := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), msg)
	b.status.Logs = append(b.status.Logs, entry)
	if len(b.status.Logs) > 20 {
		b.status.Logs = b.status.Logs[1:]
	}
	log.Printf("[%s] %s", b.config.ID, msg)
}

func (b *Bot) updateStatus(fn func(s *engine.AccountStatus)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	fn(&b.status)
}

func (b *Bot) GetStatus() engine.AccountStatus {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.status
}

func (b *Bot) ExecuteAction(action string, payload map[string]any) (any, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch action {
	case "autoHunt":
		b.config.Settings.AutoHunt = !b.config.Settings.AutoHunt
		if b.config.Settings.AutoHunt {
			b.status.Status = "hunting"
			b.status.Logs = append(b.status.Logs, "Auto-hunt ativado.")
			go b.startHunting()
		} else {
			b.status.Status = "idle"
			b.isHunting = false
			go b.client.StopHunt()
			b.status.Logs = append(b.status.Logs, "Auto-hunt pausado.")
		}
		return map[string]any{"autoHunt": b.config.Settings.AutoHunt}, nil

	case "autoHeal":
		go b.handleDefeat()
		return "Comando de cura enviado!", nil

	case "autoSell":
		b.config.Settings.AutoSell = !b.config.Settings.AutoSell
		return map[string]any{"autoSell": b.config.Settings.AutoSell}, nil

	case "autoDaily":
		go b.claimAllDailies()
		return "Coletando diárias em background...", nil

	case "switchZone":
		if zoneID, ok := payload["zoneId"].(string); ok && zoneID != "" {
			b.config.Settings.HuntTarget = zoneID
			b.isHunting = false
			go b.client.StopHunt()
			go b.startHunting()
			return fmt.Sprintf("Transitando para zona %s", zoneID), nil
		}
		return nil, fmt.Errorf("zoneId não especificado")

	case "resetCircuitBreaker":
		if b.cb != nil {
			b.cb.Reset()
			b.status.Status = "idle"
			return "Circuit Breaker restaurado manualmente!", nil
		}
		return nil, fmt.Errorf("circuit breaker não inicializado")

	case "toggleBestiaryFocus":
		b.config.Settings.BestiaryFocus = !b.config.Settings.BestiaryFocus
		statusStr := "desativado"
		if b.config.Settings.BestiaryFocus {
			statusStr = "ativado (priorizando espécies pendentes no Bestiário)"
			go b.recalculateOptimalRoute()
		}
		b.status.Logs = append(b.status.Logs, fmt.Sprintf("📖 Foco no Bestiário %s.", statusStr))
		return map[string]any{"bestiaryFocus": b.config.Settings.BestiaryFocus}, nil

	default:
		return nil, fmt.Errorf("ação desconhecida: %s", action)
	}
}

func (b *Bot) SupplyTicket(ticket, wsURL string) error {
	b.addLog(fmt.Sprintf("Recebido ticket/token de autenticação externo: %s", ticket))
	if b.client != nil {
		// Se for formato UUID (36 caracteres), tenta trocar como handoff code primeiro
		if len(ticket) == 36 && strings.Count(ticket, "-") == 4 {
			b.addLog("Detectado formato de código de handoff. Tentando troca em /auth/handoff/exchange...")
			res, err := b.client.ExchangeHandoff(ticket)
			if err == nil && res.AccessToken != "" {
				b.addLog("Sucesso! Token de acesso emitido via handoff.")
				return nil
			}
			b.addLog(fmt.Sprintf("Handoff exchange não aceitou (%v); utilizando como token direto.", err))
		}
		b.client.SetToken(ticket)
	}
	return nil
}

func (b *Bot) SupplyTurnstileToken(token string) error {
	b.addLog("Recebido Turnstile Token externo.")
	return nil
}
