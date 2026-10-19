package game

import (
	"fmt"
	"math"
	"strings"
)

// Tabela de Efetividade Elemental Pokémon (18 tipos)
// Mapeia [atacante][defensor] -> multiplicador
var TypeChart = map[string]map[string]float64{
	"normal": {
		"rock": 0.5, "ghost": 0.0, "steel": 0.5,
	},
	"fire": {
		"fire": 0.5, "water": 0.5, "grass": 2.0, "ice": 2.0, "bug": 2.0, "rock": 0.5, "dragon": 0.5, "steel": 2.0,
	},
	"water": {
		"fire": 2.0, "water": 0.5, "grass": 0.5, "ground": 2.0, "rock": 2.0, "dragon": 0.5,
	},
	"electric": {
		"water": 2.0, "electric": 0.5, "grass": 0.5, "ground": 0.0, "flying": 2.0, "dragon": 0.5,
	},
	"grass": {
		"fire": 0.5, "water": 2.0, "grass": 0.5, "poison": 0.5, "ground": 2.0, "flying": 0.5, "bug": 0.5, "rock": 2.0, "dragon": 0.5, "steel": 0.5,
	},
	"ice": {
		"fire": 0.5, "water": 0.5, "grass": 2.0, "ice": 0.5, "ground": 2.0, "flying": 2.0, "dragon": 2.0, "steel": 0.5,
	},
	"fighting": {
		"normal": 2.0, "ice": 2.0, "poison": 0.5, "flying": 0.5, "psychic": 0.5, "bug": 0.5, "rock": 2.0, "ghost": 0.0, "dark": 2.0, "steel": 2.0, "fairy": 0.5,
	},
	"poison": {
		"grass": 2.0, "poison": 0.5, "ground": 0.5, "rock": 0.5, "ghost": 0.5, "steel": 0.0, "fairy": 2.0,
	},
	"ground": {
		"fire": 2.0, "electric": 2.0, "grass": 0.5, "poison": 2.0, "flying": 0.0, "bug": 0.5, "rock": 2.0, "steel": 2.0,
	},
	"flying": {
		"electric": 0.5, "grass": 2.0, "fighting": 2.0, "bug": 2.0, "rock": 0.5, "steel": 0.5,
	},
	"psychic": {
		"fighting": 2.0, "poison": 2.0, "psychic": 0.5, "dark": 0.0, "steel": 0.5,
	},
	"bug": {
		"fire": 0.5, "grass": 2.0, "fighting": 0.5, "poison": 0.5, "flying": 0.5, "psychic": 2.0, "ghost": 0.5, "dark": 2.0, "steel": 0.5, "fairy": 0.5,
	},
	"rock": {
		"fire": 2.0, "ice": 2.0, "fighting": 0.5, "ground": 0.5, "flying": 2.0, "bug": 2.0, "steel": 0.5,
	},
	"ghost": {
		"normal": 0.0, "psychic": 2.0, "ghost": 2.0, "dark": 0.5,
	},
	"dragon": {
		"dragon": 2.0, "steel": 0.5, "fairy": 0.0,
	},
	"dark": {
		"fighting": 0.5, "psychic": 2.0, "ghost": 2.0, "dark": 0.5, "fairy": 0.5,
	},
	"steel": {
		"fire": 0.5, "water": 0.5, "electric": 0.5, "ice": 2.0, "rock": 2.0, "steel": 0.5, "fairy": 2.0,
	},
	"fairy": {
		"fire": 0.5, "fighting": 2.0, "poison": 0.5, "dragon": 2.0, "dark": 2.0, "steel": 0.5,
	},
}

// GetElementMultiplier calcula o dano efetivo de um elemento atacante contra os elementos defensores
func GetElementMultiplier(attackerElement string, defenderElements []string) float64 {
	attacker := strings.ToLower(attackerElement)
	if len(defenderElements) == 0 {
		return 1.0
	}

	chart, exists := TypeChart[attacker]
	if !exists {
		return 1.0
	}

	mult := 1.0
	for _, def := range defenderElements {
		d := strings.ToLower(def)
		if m, ok := chart[d]; ok {
			mult *= m
		}
	}
	return mult
}

// RouteScore armazena a avaliação de uma zona
type RouteScore struct {
	Zone             Zone
	Score            float64
	AverageMult      float64
	LevelDifference  int
	Reason           string
}

// FindOptimalZone escolhe a melhor zona de caça baseada no nível e elementos do líder da equipe
func FindOptimalZone(zones []Zone, leader *Creature) *Zone {
	if len(zones) == 0 {
		return nil
	}

	leaderLevel := 5
	leaderElements := []string{"normal"}
	if leader != nil {
		if leader.Level > 0 {
			leaderLevel = leader.Level
		}
		if len(leader.Elements) > 0 {
			leaderElements = leader.Elements
		}
	}

	var bestZone *Zone
	bestScore := -1.0

	for i := range zones {
		z := &zones[i]
		recLevel := z.RecommendedLevel
		if recLevel <= 0 {
			recLevel = z.MinLevel
		}
		if recLevel <= 0 {
			recLevel = 1
		}

		// Nível da zona deve ser acessível para o personagem:
		// Evitar zonas com monstros 5+ níveis acima para prevenir wipe
		if recLevel > leaderLevel+4 {
			continue
		}

		// Penalizar zonas muito abaixo do nível (XP insignificante)
		diff := leaderLevel - recLevel
		if diff > 30 && leaderLevel > 40 {
			continue
		}

		// Calcular fator elemental
		attackerElement := leaderElements[0]
		zoneElements := z.Elements
		if len(zoneElements) == 0 && z.ThemeElement != "" {
			zoneElements = []string{z.ThemeElement}
		}

		elemMult := GetElementMultiplier(attackerElement, zoneElements)

		// Pontuação base: nível da área ponderado pela vantagem elemental
		// Pontuação = recLevel * elemMult * fatorProximidade
		levelBonus := float64(recLevel)
		proximityPenalty := 1.0
		if diff < 0 {
			// Zona um pouco acima do nível (desafiadora mas viável com fraqueza)
			if elemMult >= 2.0 {
				proximityPenalty = 1.15
			} else {
				proximityPenalty = 0.8
			}
		} else if diff > 10 {
			proximityPenalty = math.Max(0.2, 1.0-(float64(diff)*0.03))
		}

		score := levelBonus * elemMult * proximityPenalty

		if score > bestScore {
			bestScore = score
			bestZone = z
		}
	}

	// Fallback para a primeira zona se nenhuma pontuou
	if bestZone == nil && len(zones) > 0 {
		bestZone = &zones[0]
	}

	return bestZone
}

// BestiaryZoneDecision encapsula a zona escolhida e o motivo detalhado
type BestiaryZoneDecision struct {
	Zone        *Zone
	SpeciesID   string
	SpeciesName string
	Remaining   int
	Threshold   int
	Reason      string
}

// FindBestiaryZone busca a melhor zona para caçar com foco em avançar o Bestiário e Metas de Espécie priorizando vantagem elemental e níveis seguros
func FindBestiaryZone(zones []Zone, leader *Creature, trainerLevel int, bestiary *BestiaryResponse, goals []SpeciesGoal) *BestiaryZoneDecision {
	if len(zones) == 0 {
		return nil
	}

	leaderLevel := 5
	leaderElements := []string{"normal"}
	if leader != nil {
		if leader.Level > 0 {
			leaderLevel = leader.Level
		}
		if len(leader.Elements) > 0 {
			leaderElements = leader.Elements
		}
	}
	attackerElement := leaderElements[0]
	if trainerLevel <= 0 {
		trainerLevel = 1
	}

	// 1. Mapear status de abates e metas por SpeciesID
	type speciesStatus struct {
		name      string
		kills     int
		nextGoal  int
		completed bool
	}
	speciesMap := make(map[string]speciesStatus)

	if bestiary != nil {
		for _, entry := range bestiary.Data {
			specID := strings.ToLower(entry.SpeciesID)
			completed := entry.NextKillThreshold <= 0 || (entry.NextKillThreshold > 0 && entry.KillCount >= entry.NextKillThreshold)
			speciesMap[specID] = speciesStatus{
				name:      entry.SpeciesName,
				kills:     entry.KillCount,
				nextGoal:  entry.NextKillThreshold,
				completed: completed,
			}
		}
	}

	for _, g := range goals {
		specID := strings.ToLower(g.SpeciesID)
		st, exists := speciesMap[specID]
		if !exists {
			st = speciesStatus{
				name:  g.SpeciesName,
				kills: g.KillCount,
			}
		}
		for _, kt := range g.KillTiers {
			if !kt.Claimed && kt.Threshold > st.kills {
				if st.nextGoal == 0 || kt.Threshold < st.nextGoal {
					st.nextGoal = kt.Threshold
					st.completed = false
				}
				break
			}
		}
		speciesMap[specID] = st
	}

	var bestDecision *BestiaryZoneDecision
	bestScore := -1.0

	// 2. Iterar por todas as zonas disponíveis
	for i := range zones {
		z := &zones[i]
		recLevel := z.RecommendedLevel
		if recLevel <= 0 {
			recLevel = z.MinLevel
		}
		if recLevel <= 0 {
			recLevel = 1
		}

		// Checagem de Nível do Treinador: mundos bloqueados por nível de treinador
		worldLower := strings.ToLower(z.World)
		if worldLower == "johto" && trainerLevel < 25 {
			continue
		}
		if worldLower == "hoenn" && trainerLevel < 50 {
			continue
		}
		if (worldLower == "outland" || worldLower == "legendary") && trainerLevel < 70 {
			continue
		}

		// Checagem de Nível do Pokémon: nunca enfrentar monstros com mais de 3 níveis acima
		if recLevel > leaderLevel+3 {
			continue
		}

		zoneElements := z.Elements
		if len(zoneElements) == 0 && z.ThemeElement != "" {
			zoneElements = []string{z.ThemeElement}
		}
		elemMult := GetElementMultiplier(attackerElement, zoneElements)

		// REJEIÇÃO ELEMENTAL: se temos desvantagem de tipo (<= 0.75x dano), ignora para evitar wipe
		if elemMult < 0.8 {
			continue
		}

		// Se o monstro estiver acima do nível do Pokémon, EXIGE vantagem elemental (>= 2.0x)
		if recLevel > leaderLevel && elemMult < 2.0 {
			continue
		}

		type zoneTarget struct {
			speciesID string
			name      string
		}
		var targets []zoneTarget

		if len(z.Encounters) > 0 {
			for _, enc := range z.Encounters {
				if enc.SpeciesID != "" {
					targets = append(targets, zoneTarget{
						speciesID: strings.ToLower(enc.SpeciesID),
						name:      enc.SpeciesID,
					})
				}
			}
		} else {
			targetName := strings.ToLower(z.ID)
			if targetName == "" {
				targetName = strings.ToLower(z.Name)
			}
			targets = append(targets, zoneTarget{
				speciesID: targetName,
				name:      z.Name,
			})
		}

		for _, tgt := range targets {
			st, known := speciesMap[tgt.speciesID]
			remaining := 0
			threshold := 0
			isCompleted := false

			if known {
				if st.name != "" {
					tgt.name = st.name
				}
				if st.completed {
					isCompleted = true
				} else if st.nextGoal > 0 {
					threshold = st.nextGoal
					remaining = st.nextGoal - st.kills
					if remaining <= 0 {
						isCompleted = true
					}
				} else {
					threshold = 100
					remaining = int(math.Max(0, float64(100-st.kills)))
				}
			} else {
				threshold = 50
				remaining = 50
			}

			if isCompleted {
				continue
			}

			// PONTUAÇÃO COM FOCO ELEMENTAL:
			// 1. Urgência base: quanto menos abates faltam para fechar o marco, maior o peso
			urgency := 1000.0 / (float64(remaining) + 10.0)

			// 2. Multiplicador quadrático de vantagem elemental:
			// 2.0x de dano -> fator 4.0x na pontuação!
			// 4.0x de dano -> fator 16.0x na pontuação!
			urgency *= (elemMult * elemMult)

			// 3. Fator de segurança de nível
			diff := leaderLevel - recLevel
			if diff < 0 {
				urgency *= 0.9 // monstro levemente superior
			} else if diff > 15 && leaderLevel > 25 {
				urgency *= 0.7 // monstro muito fraco
			}

			if urgency > bestScore {
				bestScore = urgency
				elemLabel := strings.ToUpper(strings.Join(zoneElements, "/"))
				if elemLabel == "" {
					elemLabel = "NEUTRO"
				}
				bestDecision = &BestiaryZoneDecision{
					Zone:        z,
					SpeciesID:   tgt.speciesID,
					SpeciesName: tgt.name,
					Remaining:   remaining,
					Threshold:   threshold,
					Reason: fmt.Sprintf("Foco Bestiário (Vantagem Elemental %.1fx: %s vs %s): faltam %d abates para o marco de %d (%s)",
						elemMult, strings.ToUpper(attackerElement), elemLabel, remaining, threshold, tgt.name),
				}
			}
		}
	}

	if bestDecision == nil {
		fallbackZone := FindOptimalZone(zones, leader)
		if fallbackZone != nil {
			return &BestiaryZoneDecision{
				Zone:   fallbackZone,
				Reason: "Bestiário das zonas atuais completo ou sem alvos elementais seguros; caçando em rota de XP ótima.",
			}
		}
		return nil
	}

	return bestDecision
}

