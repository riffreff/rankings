package main

import (
	"bytes"
	"context"
	"database/sql"
	"html/template"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/gorilla/mux"
	_ "github.com/mattn/go-sqlite3"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type TeamInfo struct {
	Team       int
	ParentTeam *int
	League     string
	Name       string
	Type       string
	Location   string
	Website    string
	Region     string
	Genus      string
}

func GetTeamInfo(db *sql.DB, teams []int) (map[int]TeamInfo, error) {
	ids := make([]string, len(teams))
	for i, t := range teams {
		ids[i] = strconv.FormatInt(int64(t), 10)
	}
	// Numbers can't have SQL injection.
	rows, err := db.Query(`SELECT Teams.team, Teams.parentTeam, COALESCE(Teams.league, ""), COALESCE(parent.league, ""), COALESCE(Teams.name, ""), 
         COALESCE(Teams.type, ""), COALESCE(Teams.location, ""), COALESCE(Teams.website, ""), COALESCE(Teams.region, ""), COALESCE(Teams.genus, "")
      FROM Teams 
      LEFT JOIN Teams as parent ON Teams.parentTeam = parent.team
      WHERE Teams.team in (` + strings.Join(ids, ",") + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[int]TeamInfo, len(teams))
	for rows.Next() {
		var ti TeamInfo
		var parentLeague string
		if err := rows.Scan(&ti.Team, &ti.ParentTeam, &ti.League, &parentLeague, &ti.Name, &ti.Type,
			&ti.Location, &ti.Website, &ti.Region, &ti.Genus); err != nil {
			return nil, err
		}
		if ti.ParentTeam != nil {
			ti.League = parentLeague
		}
		result[ti.Team] = ti
	}
	return result, nil
}

func GetTeamsWithGamesInPastYear(db *sql.DB, teams []int) (map[int]struct{}, error) {
	result := make(map[int]struct{}, len(teams))
	ids := make([]string, len(teams))
	for i, t := range teams {
		ids[i] = strconv.FormatInt(int64(t), 10)
	}
	// This will also catch games in the future.
	rows, err := db.Query(`SELECT homeTeam FROM Games WHERE day >= DATE("now", "-1 year") 
      AND homeTeam in (` + strings.Join(ids, ",") + `) GROUP BY homeTeam`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var team int
		if err := rows.Scan(&team); err != nil {
			return nil, err
		}
		result[team] = struct{}{}
	}

	rows, err = db.Query(`SELECT awayTeam FROM Games WHERE day >= DATE("now", "-1 year")
      AND awayTeam in (` + strings.Join(ids, ",") + `) GROUP BY homeTeam`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var team int
		if err := rows.Scan(&team); err != nil {
			return nil, err
		}
		result[team] = struct{}{}
	}
	return result, err
}

func getRanking(db *sql.DB, genus string, region string) (map[int]int, error) {
	params := []interface{}{}
	query := `SELECT Games.tournament IS NOT NULL, tourH.team is NOT NULL, tourA.team is NOT NULL, 
  Games.homeTeam, Games.awayTeam, Games.homeScore, Games.awayScore 
  FROM Games 
  JOIN Teams AS teamH ON teamH.team = Games.homeTeam
  JOIN Teams AS teamA ON teamA.team = Games.awayTeam
  LEFT JOIN TournamentHostingTeams AS tourH ON tourH.tournament = Games.tournament AND tourH.team = Games.homeTeam
  LEFT JOIN TournamentHostingTeams AS tourA ON tourA.tournament = Games.tournament AND tourA.team = Games.awayTeam
  WHERE 
  teamH.type != "Exhibition Team" AND teamA.type != "Exhibition Team"
  AND homeScore IS NOT NULL AND awayScore IS NOT NULL`

	if genus != "" {
		query += ` AND teamH.genus=? AND teamA.genus=?`
		params = append(params, genus, genus)
	}
	if region != "" {
		query += ` AND teamH.region=? AND teamA.region=?`
		params = append(params, region, region)
	}

	query += `  ORDER BY day, time, game`
	rows, err := db.Query(query, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ranker := NewRanker()
	for rows.Next() {
		gr := &GameResult{}
		if err := rows.Scan(&gr.isTournament, &gr.homeHostingTournament, &gr.awayHostingTournament,
			&gr.homeTeam, &gr.awayTeam, &gr.homeScore, &gr.awayScore); err != nil {
			return nil, err
		}
		ranker.AddGame(gr)
	}

	rankings := ranker.Rankings()

	// Filter out teams who have have had no games in the past year, and none planned.
	teams := make([]int, 0, len(rankings))
	for k, _ := range rankings {
		teams = append(teams, k)
	}
	activeTeams, err := GetTeamsWithGamesInPastYear(db, teams)
	if err != nil {
		return nil, err
	}
	activeRankings := make(map[int]int, len(activeTeams))
	for team, _ := range activeTeams {
		activeRankings[team] = rankings[team]
	}
	return activeRankings, nil
}

var (
	genera  = []string{"Women", "Men", "Open to All"}
	regions = []string{"", "Northern America", "Latin America", "Europe", "Pacific"}
)

func renderLadder(db *sql.DB, genus string, region string, w io.Writer) error {
	rankings, err := getRanking(db, genus, region)
	if err != nil {
		return err
	}

	// Sort the teams by rankings.
	ladder := make([]int, 0, len(rankings))
	for k, _ := range rankings {
		ladder = append(ladder, k)
	}
	sort.Slice(ladder, func(i, j int) bool {
		return rankings[ladder[i]] > rankings[ladder[j]]
	})

	ti, err := GetTeamInfo(db, ladder)
	if err != nil {
		return err
	}
	tmpl, err := template.ParseFiles("templates/ladder.html", "templates/common.html")
	if err != nil {
		return err
	}

	type entry struct {
		Rank     int
		Team     int
		TeamInfo TeamInfo
		Rating   float64
	}
	data := struct {
		Regions  []string
		Region   string
		Genera   []string
		Genus    string
		Rankings []entry
	}{
		Region:  region,
		Regions: regions,
		Genus:   genus,
		Genera:  genera,
	}
	data.Rankings = make([]entry, len(ladder))
	for i, team := range ladder {
		data.Rankings[i] = entry{
			Rank:     i + 1,
			Team:     team,
			TeamInfo: ti[team],
			Rating:   float64(rankings[team]) / 10,
		}
	}
	return tmpl.Execute(w, data)
}

var (
	cacheMu = sync.Mutex{}
	cache   = map[string]string{}
)

func serveLadder(logger log.Logger, db *sql.DB, w http.ResponseWriter, r *http.Request) {
	genus := r.URL.Query().Get("genus")
	if _, ok := r.URL.Query()["genus"]; !ok {
		genus = "Women" // No paramater specified.
	}
	region := r.URL.Query().Get("region")

	key := genus + "#" + region

	cacheMu.Lock()
	str, ok := cache[key]
	cacheMu.Unlock()

	if ok {
		w.Write([]byte(str))
		return
	}

	buf := &bytes.Buffer{}
	err := renderLadder(db, genus, region, buf)
	if err != nil {
		level.Error(logger).Log("err", err)
		http.Error(w, err.Error(), 500)
		return
	}
	str = string(buf.Bytes())

	cacheMu.Lock()
	cache[key] = str
	cacheMu.Unlock()
	w.Write([]byte(str))
}

func main() {
	logger := log.NewLogfmtLogger(log.NewSyncWriter(os.Stdout))
	db, err := sql.Open("sqlite3", "./rankings.db?_mutex=full&_journal_mode=WAL")
	if err != nil {
		level.Error(logger).Log("msg", "Error opening database", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	r := mux.NewRouter()
	r.Handle("/metrics", promhttp.Handler())

	r.Path("/").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveLadder(logger, db, w, r)
	})

	term := make(chan os.Signal, 1)
	signal.Notify(term, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	listenAddr := ":8003"
	level.Info(logger).Log("msg", "Listening for HTTP", "addr", listenAddr)
	httpSrv := &http.Server{Addr: listenAddr, Handler: r}
	go func() {
		if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
			level.Error(logger).Log("msg", "Error listening for HTTP", "err", err)
			os.Exit(1)
		}
	}()

	s := <-term
	level.Info(logger).Log("msg", "Shutting down due to signal", "signal", s)
	go httpSrv.Shutdown(context.Background()) // Stop accepting new connections.
	level.Info(logger).Log("msg", "Shutdown complete. Exiting.")
	os.Exit(0)

}
