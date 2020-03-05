package main

import (
	"math"
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
}

func NewRanker() *Ranker {
	return &Ranker{
		teams: map[int]int{},
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
	// Allow for home team advantage.
	homeAdj := 0
	if !gr.isTournament {
		homeAdj = +200
	} else {
		// Consider anyone hosting a tournament as at home.
		if gr.homeHostingTournament && !gr.awayHostingTournament {
			homeAdj = +200
		} else if !gr.homeHostingTournament && gr.awayHostingTournament {
			homeAdj = -200
		}
	}
	predictedDos := -1 + 2/(1+math.Exp(float64(rankA-rankH-homeAdj)/1000))
	dos := float64(gr.homeScore-gr.awayScore) / float64(gr.homeScore+gr.awayScore)

	adj := int((dos - predictedDos) * 600)

	if okH && okA {
		r.teams[gr.homeTeam] += adj
		r.teams[gr.awayTeam] -= adj
	}
	// If one team was unranked, only set that team.
	if !okA {
		r.teams[gr.awayTeam] = rankA + adj
	}
	if !okH {
		r.teams[gr.homeTeam] = rankH + adj
	}
}

func (r Ranker) Rankings() map[int]int {
	rankings := make(map[int]int, len(r.teams))
	for k, v := range r.teams {
		rankings[k] = v
	}
	return rankings
}
