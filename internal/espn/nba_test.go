package espn

import "testing"

func TestNormalizeNBAGameStateMapsPossessionFromCompetitors(t *testing.T) {
	summary := nbaSummaryResponse{}
	comp := struct {
		Date   string `json:"date"`
		Status struct {
			Type struct {
				Name        string `json:"name"`
				State       string `json:"state"`
				Detail      string `json:"detail"`
				Description string `json:"description"`
				Completed   bool   `json:"completed"`
			} `json:"type"`
			Period int `json:"period"`
		} `json:"status"`
		Competitors []struct {
			HomeAway   string `json:"homeAway"`
			Score      string `json:"score"`
			Possession bool   `json:"possession"`
			Team       struct {
				ID           string `json:"id"`
				Abbreviation string `json:"abbreviation"`
			} `json:"team"`
		} `json:"competitors"`
		Situation *struct {
			Possession string `json:"possession"`
		} `json:"situation"`
	}{}
	comp.Status.Type.State = "in"
	comp.Status.Type.Detail = "2:25 - 1st Quarter"
	comp.Status.Period = 1

	home := struct {
		HomeAway   string `json:"homeAway"`
		Score      string `json:"score"`
		Possession bool   `json:"possession"`
		Team       struct {
			ID           string `json:"id"`
			Abbreviation string `json:"abbreviation"`
		} `json:"team"`
	}{HomeAway: "home", Score: "10"}
	home.Team.Abbreviation = "OKC"

	away := struct {
		HomeAway   string `json:"homeAway"`
		Score      string `json:"score"`
		Possession bool   `json:"possession"`
		Team       struct {
			ID           string `json:"id"`
			Abbreviation string `json:"abbreviation"`
		} `json:"team"`
	}{HomeAway: "away", Score: "8", Possession: true}
	away.Team.Abbreviation = "LAL"

	comp.Competitors = append(comp.Competitors, home, away)
	summary.Header.Competitions = append(summary.Header.Competitions, comp)

	state := normalizeNBAGameState("401810971", summary)
	if state.Possession != "LAL" {
		t.Fatalf("expected possession to be LAL, got %q", state.Possession)
	}
}
