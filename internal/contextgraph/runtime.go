// Context and graph persistence are independent of model execution. The single
// streamed runtime in internal/agent remains the only model/tool controller.
package contextgraph

import (
	"code-editor/internal/agent"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

type Session struct {
	Version   int                 `json:"version"`
	Task      string              `json:"task"`
	Draft     string              `json:"draft,omitempty"`
	Notes     []string            `json:"notes,omitempty"`
	Included  []string            `json:"included,omitempty"`
	Status    string              `json:"status"`
	Evidence  Evidence            `json:"evidence"`
	Reward    Reward              `json:"reward"`
	Rewards   []Reward            `json:"rewards,omitempty"`
	HighWater int                 `json:"high_water"`
	Checks    []agent.CheckResult `json:"checks,omitempty"`
}
type View struct {
	Session                                               Session
	GraphLines                                            []string
	GraphFiles, GraphSymbols, GraphFindings, GraphQuality int
	GraphHash                                             string
	Stats                                                 ScanStats
	Busy                                                  bool
	Error                                                 string
	ProviderContext                                       string
}
type Request struct{ Kind, Text string }
type queuedRequest struct {
	request Request
	epoch   uint64
}

type Service struct {
	cancelEpoch     atomic.Uint64
	root, stateHome string
	commands        chan queuedRequest
	events          chan View
	done            chan struct{}
	ctx             context.Context
	stop            context.CancelFunc
	busy            atomic.Bool
	mu              sync.Mutex
	activeCancel    context.CancelFunc
	closeErr        error
}

func Start(root, stateHome string) *Service {
	ctx, stop := context.WithCancel(context.Background())
	s := &Service{root: root, stateHome: stateHome, commands: make(chan queuedRequest, 1), events: make(chan View, 1), done: make(chan struct{}), ctx: ctx, stop: stop}
	s.busy.Store(true)
	go s.loop()
	return s
}
func (s *Service) Events() <-chan View { return s.events }
func (s *Service) Busy() bool          { return s.busy.Load() }
func (s *Service) Submit(r Request) bool {
	epoch := s.cancelEpoch.Load()
	if s.ctx.Err() != nil || len(r.Text) > 8192 || !s.busy.CompareAndSwap(false, true) {
		return false
	}
	select {
	case s.commands <- queuedRequest{request: r, epoch: epoch}:
		return true
	default:
		s.busy.Store(false)
		return false
	}
}
func (s *Service) Cancel() {
	s.cancelEpoch.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeCancel != nil {
		s.activeCancel()
	}
}
func (s *Service) Close() error {
	s.stop()
	select {
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.closeErr
	case <-time.After(3 * time.Second):
		return errors.New("context checkpoint shutdown exceeded limit")
	}
}
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
func redact(s string) string {
	for _, name := range []string{"KIWICODE_AGENT_API_KEY", "KIWICODE_AGENT_KEY"} {
		if key := os.Getenv(name); key != "" {
			s = strings.ReplaceAll(s, key, "[REDACTED]")
		}
	}
	return s
}

type worker struct {
	service         *Service
	store           *Store
	session         Session
	graph, baseline *Graph
	stats           ScanStats
	graphLines      []string
}

func (w *worker) publish(err error) {
	session := w.session
	session.Notes = append([]string(nil), session.Notes...)
	session.Included = append([]string(nil), session.Included...)
	session.Checks = append([]agent.CheckResult(nil), session.Checks...)
	session.Rewards = append([]Reward(nil), session.Rewards...)
	v := View{Session: session, Stats: w.stats, GraphLines: w.graphLines, Busy: w.service.Busy()}
	if err != nil {
		v.Error = redact(err.Error())
	}
	if w.graph != nil {
		v.GraphHash = w.graph.Hash
		v.GraphFiles = len(w.graph.Files)
		for _, f := range w.graph.Files {
			v.GraphSymbols += len(f.Symbols)
			v.GraphFindings += len(f.Findings)
			v.GraphQuality += len(f.Quality)
		}
	}
	// Only explicit notes and included metadata may become provider context.
	// Source bytes are still requested through the main runtime's read approval.
	payload := struct {
		Notes    []string
		Included []string
		Graph    map[string]*File
		Omitted  int
	}{Notes: session.Notes, Included: session.Included, Graph: map[string]*File{}}
	for _, path := range session.Included {
		if w.graph == nil || w.graph.Files[path] == nil {
			continue
		}
		payload.Graph[path] = w.graph.Files[path]
		data, _ := json.Marshal(payload)
		if len(data) > 16<<10 {
			delete(payload.Graph, path)
			payload.Omitted++
		}
	}
	data, _ := json.Marshal(payload)
	if len(session.Notes) > 0 || len(session.Included) > 0 {
		v.ProviderContext = string(data)
	}
	select {
	case w.service.events <- v:
	default:
		select {
		case <-w.service.events:
		default:
		}
		w.service.events <- v
	}
}
func (w *worker) save() error { return w.store.Save("session.json", w.session, MaxStateBytes) }
func validateGraph(g *Graph) error {
	if g.Version != SchemaVersion || len(g.Files) > MaxFiles {
		return errors.New("unsupported or oversized graph")
	}
	for p, f := range g.Files {
		if f == nil || p != f.Path || !AllowedPath(p) {
			return errors.New("invalid graph record")
		}
	}
	return nil
}
func validateSession(s Session) error {
	if s.Version != SchemaVersion || len(s.Notes) > 64 || len(s.Included) > 32 || len(s.Task) > 8192 || len(s.Draft) > 8192 || s.HighWater < 0 || len(s.Rewards) > 64 || len(s.Checks) > 2 {
		return errors.New("unsupported or oversized context checkpoint")
	}
	total := 0
	for _, n := range s.Notes {
		total += len(n)
		if len(n) > 2048 {
			return errors.New("note too large")
		}
	}
	if total > 8192 {
		return errors.New("context note budget exceeded")
	}
	for _, p := range s.Included {
		if !AllowedPath(p) {
			return errors.New("invalid included path")
		}
	}
	return nil
}
func (s *Service) loop() {
	defer close(s.done)
	defer s.stop()
	w := &worker{service: s, session: Session{Version: SchemaVersion, Status: "ready"}}
	fail := func(err error) {
		w.session.Status = "unavailable"
		s.busy.Store(false)
		w.publish(err)
		s.mu.Lock()
		s.closeErr = err
		s.mu.Unlock()
	}
	absolute, err := filepath.Abs(s.root)
	if err != nil {
		fail(err)
		return
	}
	s.root, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		fail(err)
		return
	}
	w.store, err = OpenStore(s.root, s.stateHome)
	if err != nil {
		fail(err)
		return
	}
	defer w.store.Close()
	var saved Session
	err = w.store.Load("session.json", &saved, MaxStateBytes)
	if err != nil && !os.IsNotExist(err) {
		fail(err)
		return
	}
	if err == nil {
		w.session = saved
	}
	if err = validateSession(w.session); err != nil {
		fail(err)
		return
	}
	for name, target := range map[string]**Graph{"graph.json": &w.graph, "baseline.json": &w.baseline} {
		var g Graph
		err = w.store.Load(name, &g, MaxGraphBytes)
		if os.IsNotExist(err) {
			continue
		}
		if err == nil {
			err = validateGraph(&g)
		}
		if err != nil {
			fail(err)
			return
		}
		*target = &g
	}
	if w.session.Status == "checking" || w.session.Status == "indexing" {
		w.session.Status = "interrupted"
		w.session.Evidence = Evidence{}
	}
	w.buildGraphLines()
	s.busy.Store(false)
	w.publish(nil)
	for {
		select {
		case <-s.ctx.Done():
			return
		case r := <-s.commands:
			ctx, cancel := context.WithTimeout(s.ctx, 3*time.Minute)
			s.mu.Lock()
			s.activeCancel = cancel
			s.mu.Unlock()
			if r.epoch != s.cancelEpoch.Load() {
				cancel()
			}
			err = ctx.Err()
			if err == nil {
				err = w.handle(ctx, r.request)
			}
			cancel()
			s.mu.Lock()
			s.activeCancel = nil
			s.mu.Unlock()
			if err != nil {
				w.session.Status = "failed"
				if errors.Is(err, context.Canceled) {
					w.session.Status = "cancelled"
				}
			}
			if saveErr := w.save(); saveErr != nil {
				err = saveErr
				s.mu.Lock()
				s.closeErr = saveErr
				s.mu.Unlock()
				w.session.Status = "persistence error"
			}
			s.busy.Store(false)
			w.publish(err)
		}
	}
}
func (w *worker) index(ctx context.Context) error {
	w.session.Status = "indexing"
	w.publish(nil)
	g, stats, err := Scan(ctx, w.service.root, w.graph, filepath.Dir(filepath.Dir(w.store.Dir)))
	if err != nil {
		return err
	}
	if w.graph == nil || w.graph.Hash != g.Hash {
		if err = w.store.Save("graph.json", g, MaxGraphBytes); err != nil {
			return err
		}
	}
	if w.baseline == nil {
		if err = w.store.Save("baseline.json", g, MaxGraphBytes); err != nil {
			return err
		}
		w.baseline = g
	}
	w.graph, w.stats = g, stats
	w.buildGraphLines()
	w.session.Status = "ready"
	return nil
}
func (w *worker) handle(ctx context.Context, r Request) error {
	text := strings.TrimSpace(r.Text)
	switch r.Kind {
	case "index":
		return w.index(ctx)
	case "task":
		if text == "" || len(text) > 8192 {
			return errors.New("task must be 1..8192 bytes")
		}
		w.session.Task = redact(text)
	case "remember":
		n := len(text)
		for _, note := range w.session.Notes {
			n += len(note)
		}
		if text == "" || len(text) > 2048 || len(w.session.Notes) >= 64 || n > 8192 {
			return errors.New("context notes limited to 8 KiB total, 64 notes, 2 KiB per note")
		}
		w.session.Notes = append(w.session.Notes, redact(text))
	case "include":
		if !AllowedPath(text) {
			return errors.New("source path not allowed")
		}
		if _, err := ReadSource(w.service.root, text); err != nil {
			return err
		}
		for _, p := range w.session.Included {
			if p == text {
				return nil
			}
		}
		if len(w.session.Included) >= 32 {
			return errors.New("context limited to 32 included files")
		}
		w.session.Included = append(w.session.Included, text)
		sort.Strings(w.session.Included)
	case "exclude":
		var paths []string
		for _, p := range w.session.Included {
			if p != text {
				paths = append(paths, p)
			}
		}
		w.session.Included = paths
	case "forget":
		w.session.Notes = nil
		w.session.Included = nil
		w.session.Task = ""
		w.session.Draft = ""
		w.session.Checks = nil
		w.session.Evidence = Evidence{}
	case "check":
		return w.check(ctx)
	case "reward":
		if err := w.index(ctx); err != nil {
			return err
		}
		evidence := w.session.Evidence
		evidence.Reviewed = true
		reward := Evaluate(w.baseline, w.graph, evidence, w.session.HighWater)
		w.session.Reward = reward
		if strings.HasPrefix(reward.Reason, "verified") {
			w.session.Evidence = evidence
			w.session.HighWater = max(w.session.HighWater, reward.Score)
			seen := false
			for _, r := range w.session.Rewards {
				seen = seen || r.Snapshot == reward.Snapshot && r.Policy == reward.Policy
			}
			if !seen {
				w.session.Rewards = append(w.session.Rewards, reward)
				if len(w.session.Rewards) > 64 {
					w.session.Rewards = append([]Reward(nil), w.session.Rewards[len(w.session.Rewards)-64:]...)
				}
			}
		}
	default:
		return errors.New("unknown context operation")
	}
	w.session.Status = "ready"
	return nil
}
func (w *worker) check(ctx context.Context) error {
	w.session.Checks = nil
	w.session.Evidence = Evidence{}
	if err := w.index(ctx); err != nil {
		return err
	}
	before := w.graph.Hash
	paths := make([]string, 0, len(w.graph.Files))
	for p := range w.graph.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	w.session.Status = "checking"
	if err := w.save(); err != nil {
		return err
	}
	w.publish(nil)
	for _, argv := range [][]string{{"go", "test", "-count=1", "./..."}, {"go", "vet", "./..."}} {
		// The UI requires opt-in plus a second exact /check confirmation. Use the
		// existing snapshot runner, NEVER the old feature's live-worktree executor.
		result, err := agent.CheckWorkspace(ctx, w.service.root, paths, argv)
		result.Output = redact(result.Output)
		result.Error = redact(result.Error)
		w.session.Checks = append(w.session.Checks, result)
		w.publish(nil)
		if err != nil {
			return err
		}
		if result.Error != "" || result.ExitCode != 0 {
			return fmt.Errorf("%s failed: %s", strings.Join(argv, " "), result.Error)
		}
	}
	if err := w.index(ctx); err != nil {
		return err
	}
	if before != w.graph.Hash {
		return errors.New("source changed during checks; evidence discarded")
	}
	w.session.Evidence = Evidence{Snapshot: before, TestsPassed: true, VetPassed: true}
	w.session.Status = "review"
	return nil
}
func (w *worker) buildGraphLines() {
	w.graphLines = nil
	if w.graph == nil {
		return
	}
	paths := make([]string, 0, len(w.graph.Files))
	for p := range w.graph.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	add := func(line string) bool {
		if len(w.graphLines) >= 1024 {
			return false
		}
		w.graphLines = append(w.graphLines, clip(line, 2048))
		return true
	}
	for _, p := range paths {
		f := w.graph.Files[p]
		if !add(p) {
			break
		}
		for _, sym := range f.Symbols {
			if !add(fmt.Sprintf("  %s %s @ %d; complexity %d", sym.Kind, sym.Name, sym.Line, sym.Complexity)) {
				break
			}
		}
		for _, edge := range f.Edges {
			if edge.Kind != "declares" {
				if !add("  " + edge.Kind + " -> " + edge.To) {
					break
				}
			}
		}
		for _, debt := range f.Findings {
			if !add(fmt.Sprintf("  debt %s @ %d; excess %d", debt.Rule, debt.Line, debt.Excess)) {
				break
			}
		}
		if f.ParseError {
			add("  PARSE ERROR: not eligible for reward")
		}
	}
	if len(w.graphLines) == 1024 {
		w.graphLines = append(w.graphLines, "Display capped; persisted graph contains remaining records.")
	}
}
