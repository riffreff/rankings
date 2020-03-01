package main

import (
	"github.com/kortemy/elo-go"
)

type GameResult struct {
	homeTeam  int
	homeScore int
	awayTeam  int
	awayScore int

	isTournament          bool
	homeHostingTournament bool
	awayHostingTournament bool
}

type Ranker struct {
	teams map[int]int
	elo   elogo.Elo
}

func NewRanker() *Ranker {
	return &Ranker{
		teams: map[int]int{},
		elo:   *elogo.NewEloWithFactors(1000, 2000),
	}
}

func (r *Ranker) AddGame(gr *GameResult) {
	// Avoid divsion by 0.
	if gr.homeScore == 0 && gr.awayScore == 0 {
		gr.homeScore = 1
		gr.awayScore = 1
	}
	rankH, okH := r.teams[gr.homeTeam]
	rankA, okA := r.teams[gr.awayTeam]
	// If only one team is ranked, assume the other has the same rank.
	// If neither is ranked, use 7500.
	if !okH && okA {
		rankH = rankA
	} else if okH && !okA {
		rankA = rankH
	} else if !okH && !okA {
		rankH = 7500
		rankA = 7500
	}
	dos := float64(gr.homeScore) / float64(gr.homeScore+gr.awayScore)
	if !gr.isTournament {
		// Allow for home team advantage.
		dos -= .03
	} else {
		// Considering anyone hosting a tournament as at home.
		if gr.homeHostingTournament && !gr.awayHostingTournament {
			dos -= .03
		} else if !gr.homeHostingTournament && gr.awayHostingTournament {
			dos += .03
		}
	}
	h, a := r.elo.Outcome(rankH, rankA, dos)

	if okH && okA {
		r.teams[gr.homeTeam] = h.Rating
		r.teams[gr.awayTeam] = a.Rating
	}
	// If one team was unranked, only set that team and over-apply the
	// change to allow for not having history.
	if !okA {
		r.teams[gr.awayTeam] = a.Rating + a.Delta*2
	}
	if !okH {
		r.teams[gr.homeTeam] = h.Rating + h.Delta*2
	}
}

func (r Ranker) Rankings() map[int]int {
	rankings := make(map[int]int, len(r.teams))
	for k, v := range r.teams {
		rankings[k] = v
	}
	return rankings
}
