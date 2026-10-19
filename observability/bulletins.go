package observability

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Bulletin severities.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
	SeverityInfo    = "info"
)

// Bulletin components name where in a flow a problem happened. Stage and
// sink components carry the stage ID or 0-based sink index.
const (
	ComponentSource     = "source"
	ComponentFramer     = "framer"
	ComponentParser     = "parser"
	ComponentDeadLetter = "deadLetter"
	ComponentRuntime    = "runtime"
	ComponentTap        = "tap"
)

// ComponentStage is the component of a stage, by its ID.
func ComponentStage(id string) string { return "stage:" + id }

// ComponentSink is the component of the i-th destination (0-based).
func ComponentSink(i int) string { return "sink:" + strconv.Itoa(i) }

// MaxBulletinsPerFlow bounds each flow's board. Aggregation by (component,
// message) keeps repeats in one entry; when a flow has more distinct problems
// than this, the least recently seen are forgotten.
const MaxBulletinsPerFlow = 200

// maxBulletinMessage bounds the stored message text.
const maxBulletinMessage = 500

// Bulletin is one aggregated problem a flow reported: every occurrence of
// the same message from the same component.
type Bulletin struct {
	ID        string    `json:"id"`
	Flow      string    `json:"flow"`
	Severity  string    `json:"severity"`
	Component string    `json:"component"`
	Message   string    `json:"message"`
	Count     uint64    `json:"count"`
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
}

// BulletinID identifies a (flow, component, message) aggregate. It is stable
// across processes, so the controller can acknowledge a bulletin it read
// from a flow runtime.
func BulletinID(flow, component, message string) string {
	sum := sha256.Sum256([]byte(flow + "\x00" + component + "\x00" + message))
	return hex.EncodeToString(sum[:8])
}

var bulletinsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gofi_bulletins_total", Help: "Problems reported to a flow's bulletin board, by severity."}, []string{"listener", "severity"})

func init() { registry.MustRegister(bulletinsTotal) }

var board = struct {
	sync.Mutex
	byFlow map[string]map[string]*Bulletin
}{byFlow: map[string]map[string]*Bulletin{}}

// Report records one problem on the flow's bulletin board. Message text is
// truncated; callers must not put message content or credentials in it
// beyond what they already log.
func Report(flow, severity, component, message string) {
	if flow == "" {
		return
	}
	message = truncateText(strings.TrimSpace(message), maxBulletinMessage)
	id := BulletinID(flow, component, message)
	now := time.Now()
	bulletinsTotal.WithLabelValues(flow, severity).Inc()
	board.Lock()
	defer board.Unlock()
	flowBoard := board.byFlow[flow]
	if flowBoard == nil {
		flowBoard = map[string]*Bulletin{}
		board.byFlow[flow] = flowBoard
	}
	if b := flowBoard[id]; b != nil {
		b.Count++
		b.LastSeen = now
		if severityRank(severity) > severityRank(b.Severity) {
			b.Severity = severity
		}
		return
	}
	if len(flowBoard) >= MaxBulletinsPerFlow {
		var oldest *Bulletin
		for _, b := range flowBoard {
			if oldest == nil || b.LastSeen.Before(oldest.LastSeen) {
				oldest = b
			}
		}
		delete(flowBoard, oldest.ID)
	}
	flowBoard[id] = &Bulletin{ID: id, Flow: flow, Severity: severity, Component: component, Message: message, Count: 1, FirstSeen: now, LastSeen: now}
}

// ReportError records err (if any) as an error bulletin.
func ReportError(flow, component string, err error) {
	if err != nil {
		Report(flow, SeverityError, component, err.Error())
	}
}

// Bulletins returns a flow's bulletins (every flow's when flow is empty),
// most recently seen first.
func Bulletins(flow string) []Bulletin {
	board.Lock()
	var out []Bulletin
	for name, flowBoard := range board.byFlow {
		if flow != "" && name != flow {
			continue
		}
		for _, b := range flowBoard {
			out = append(out, *b)
		}
	}
	board.Unlock()
	SortBulletins(out)
	return out
}

// ClearBulletins forgets bulletins: the given IDs of a flow, every bulletin
// of the flow when ids is empty.
func ClearBulletins(flow string, ids ...string) {
	board.Lock()
	defer board.Unlock()
	if len(ids) == 0 {
		delete(board.byFlow, flow)
		return
	}
	for _, id := range ids {
		delete(board.byFlow[flow], id)
	}
}

// SortBulletins orders bulletins most recently seen first.
func SortBulletins(items []Bulletin) {
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].LastSeen.Equal(items[j].LastSeen) {
			return items[i].LastSeen.After(items[j].LastSeen)
		}
		return items[i].ID < items[j].ID
	})
}

func severityRank(severity string) int {
	switch severity {
	case SeverityError:
		return 2
	case SeverityWarning:
		return 1
	}
	return 0
}

// RouteComponent maps a route ID (RouteSink, RouteSource, RouteDeadLetter)
// to its bulletin component.
func RouteComponent(route string) string {
	switch route {
	case RouteSource:
		return ComponentSource
	case RouteDeadLetter:
		return ComponentDeadLetter
	}
	if index, ok := strings.CutPrefix(route, "egress-sink-"); ok {
		return "sink:" + index
	}
	return route
}
