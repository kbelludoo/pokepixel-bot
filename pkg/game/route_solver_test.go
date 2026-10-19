package game

import (
	"testing"
)

func TestTypeChartMultipliers(t *testing.T) {
	tests := []struct {
		attacker string
		defender []string
		expected float64
	}{
		{"fire", []string{"grass"}, 2.0},
		{"water", []string{"fire"}, 2.0},
		{"water", []string{"grass"}, 0.5},
		{"electric", []string{"ground"}, 0.0},
		{"fire", []string{"grass", "steel"}, 4.0}, // Dupla fraqueza
		{"normal", []string{"ghost"}, 0.0},
		{"unknown", []string{"fire"}, 1.0},
	}

	for _, tt := range tests {
		got := GetElementMultiplier(tt.attacker, tt.defender)
		if got != tt.expected {
			t.Errorf("GetElementMultiplier(%s, %v) = %f; esperado %f", tt.attacker, tt.defender, got, tt.expected)
		}
	}
}

func TestFindOptimalZone(t *testing.T) {
	zones := []Zone{
		{
			ID:               "zone_grass_easy",
			Name:             "Floresta Inicial",
			RecommendedLevel: 5,
			Elements:         []string{"grass"},
		},
		{
			ID:               "zone_water_mid",
			Name:             "Lago Azul",
			RecommendedLevel: 10,
			Elements:         []string{"water"},
		},
		{
			ID:               "zone_grass_mid",
			Name:             "Selva Profunda",
			RecommendedLevel: 10,
			Elements:         []string{"grass"},
		},
		{
			ID:               "zone_hard",
			Name:             "Caverna do Dragão",
			RecommendedLevel: 100,
			Elements:         []string{"dragon"},
		},
	}

	// Treinador com Charmander (Fire, Lv. 9)
	charmander := &Creature{
		Name:     "Charmander",
		Level:    9,
		Elements: []string{"fire"},
	}

	best := FindOptimalZone(zones, charmander)
	if best == nil {
		t.Fatalf("Esperava encontrar uma zona ótima, recebeu nil")
	}

	// Com Charmander Lv.9, zona de nível 10 com elemento Grass (fraqueza x2.0 a fogo) deve ser a melhor
	if best.ID != "zone_grass_mid" {
		t.Errorf("Zona escolhida foi %s (%s); esperado zone_grass_mid", best.ID, best.Name)
	}

	// Zona de dragão Lv 100 deve ser ignorada para evitar wipe
	if best.ID == "zone_hard" {
		t.Errorf("Zona de alto risco não deveria ser escolhida")
	}
}
