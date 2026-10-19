# ⚡ PokePixel Idle Autonomous Multi-Bot & Live Dashboard (Go Native Engine)

Bot autônomo de altíssima performance desenvolvido em **Go (Golang)** puro para o MMORPG idle **PokePixel** (`https://pokepixel.nietore.com/play/`).

🌐 **Dashboard Live (GitHub Pages):** [https://kbelludoo.github.io/pokepixel-bot/](https://kbelludoo.github.io/pokepixel-bot/)  
⚡ **Central Command Center:** [https://kbelludoo.github.io/bot-template-go/?v=force1](https://kbelludoo.github.io/bot-template-go/?v=force1)

---

## 🌟 Funcionalidades Principais

- ⚔️ **Caçada e Combate Autoritativo:** Protocolo WebSocket (`huntsim.enter`, `ping`, `hunt.resume`) integrado ao ciclo de combate em tempo real.
- 🔄 **Dynamic Route Solver:** Matriz elementar de 18 tipos Pokémon para cálculo automático de fraquezas e vantagens (ex: Squirtle Água causando 2.0x dano contra Pokémon de Fogo).
- 🎯 **Auto-Capture Inteligente:** Captura automática de Pokémon selvagens com detecção prioritária de Shinies.
- 🛡️ **Anti-Ban Circuit Breaker:** Desarme imediato em HTTP 429/403 ou heurísticas do servidor com backoff de 15 a 30 minutos e retorno a zona segura.
- 🎁 **Automações de Suporte (Faz Tudo):**
  - Resgate de Login Diário, Farm Offline e Stamina
  - Resgate de Missões, Metas da Pokédex e Passe de Batalha
  - Coleta automática de cartas e anexos do Correio
  - Chocadeira de ovos automática
  - Venda e extração automática de itens no NPC
  - Auto-Heal e visita gratuita à Enfermeira Joy (Centro Pokémon)
- 🪶 **Consumo Mínimo na VPS:** Apenas **~1.6 MB de RAM**, sem navegadores (zero Puppeteer/Chromium), processo Go nativo.
- 🔒 **Modo Stealth & Segurança:** Sanitização total de nicks, e-mails e IPs antes de qualquer sincronização com o GitHub Pages.

---

## 🚀 Como Executar

### 1. No Windows (Local):
```powershell
.\pokepixel-bot.exe
```
Dashboard local em: `http://localhost:3001`

### 2. Na VPS Linux (24/7 via Systemd):
```bash
sudo systemctl status pokepixel.service
journalctl -u pokepixel.service -f
```
Dashboard na VPS em: `http://localhost:3015`
