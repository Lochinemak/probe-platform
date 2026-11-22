package server

import (
	"net/http"
	"sync"
	"time"
)

// Stats is the summary on the probe page: how many nodes there are and where,
// and how busy the last day was.
type Stats struct {
	Nodes struct {
		Total    int `json:"total"`    // nodes that have connected at least once
		Online   int `json:"online"`   //
		Mainland int `json:"mainland"` // online, in mainland China
		Overseas int `json:"overseas"` // online, anywhere else (Hong Kong, Macau and Taiwan included)
		Unknown  int `json:"unknown"`  // online, place not known
		Regions  int `json:"regions"`  // distinct provinces / countries with an online node
	} `json:"nodes"`
	Tasks24h       int `json:"tasks_24h"`        // probes people started
	MonitorRuns24h int `json:"monitor_runs_24h"` // scheduled monitor runs
	// SuccessRate24h is the share of per-node results in the last day that
	// completed (0..1), nil without any. "Completed" is about the node doing
	// its job: a ping that ran and saw 100% loss still counts.
	SuccessRate24h *float64 `json:"success_rate_24h"`
}

const statsTTL = 30 * time.Second

var statsCache struct {
	sync.Mutex
	at  time.Time
	val *Stats
}

// invalidateStats makes the next request recompute, after a change the summary
// should show at once.
func invalidateStats() {
	statsCache.Lock()
	statsCache.val = nil
	statsCache.Unlock()
}

func (a *API) stats(w http.ResponseWriter, r *http.Request) {
	statsCache.Lock()
	defer statsCache.Unlock()
	if statsCache.val == nil || time.Since(statsCache.at) > statsTTL {
		st, err := a.computeStats()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		statsCache.val, statsCache.at = st, time.Now()
	}
	writeJSON(w, http.StatusOK, statsCache.val)
}

func (a *API) computeStats() (*Stats, error) {
	var st Stats
	regions := map[string]bool{}
	for _, ag := range a.hub.Agents() {
		if ag.Auth == "" && !ag.Online {
			continue // created on the dashboard, never connected
		}
		st.Nodes.Total++
		if !ag.Online {
			continue
		}
		st.Nodes.Online++
		switch g := ag.Geo; {
		case g == nil || g.CC == "":
			st.Nodes.Unknown++
		case isMainland(g):
			st.Nodes.Mainland++
			regions["CN/"+g.Province] = true
		default:
			st.Nodes.Overseas++
			regions[g.CC+"/"+g.Province] = true
		}
	}
	st.Nodes.Regions = len(regions)
	since := time.Now().Add(-24 * time.Hour)
	var err error
	if st.Tasks24h, st.MonitorRuns24h, err = a.store.CountTasksSince(since); err != nil {
		return nil, err
	}
	done, total, err := a.store.CountResultsSince(since)
	if err != nil {
		return nil, err
	}
	if total > 0 {
		rate := float64(done) / float64(total)
		st.SuccessRate24h = &rate
	}
	return &st, nil
}

// CountTasksSince counts tasks created since t: those somebody started, and
// scheduled monitor runs.
func (s *Store) CountTasksSince(t time.Time) (adhoc, monitor int, err error) {
	err = s.db.QueryRow(`SELECT COALESCE(SUM(monitor_id=''),0), COALESCE(SUM(monitor_id!=''),0) FROM tasks WHERE created_at>=?`, unixMs(t)).Scan(&adhoc, &monitor)
	return
}

// CountResultsSince counts the finished per-node results of tasks created
// since t, and how many of them completed.
func (s *Store) CountResultsSince(t time.Time) (done, total int, err error) {
	err = s.db.QueryRow(`SELECT COALESCE(SUM(r.status='done'),0), COUNT(*) FROM results r JOIN tasks t ON t.id=r.task_id
		WHERE t.created_at>=? AND r.status IN ('done','error')`, unixMs(t)).Scan(&done, &total)
	return
}
