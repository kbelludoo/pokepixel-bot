package game

import (
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
