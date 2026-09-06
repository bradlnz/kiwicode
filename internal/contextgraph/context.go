// Package agent owns local context, a syntactic code graph and verified quality
// signals. No model call is required to index a workspace or inspect its debt.
package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

const (
	SchemaVersion   = 1
	MaxFileBytes    = 2 << 20
	MaxContextBytes = 128 << 10
	MaxStateBytes   = 8 << 20
	MaxGraphBytes   = 64 << 20
	MaxFiles        = 20000
)

type Symbol struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Line       int    `json:"line"`
	End        int    `json:"end"`
	Complexity int    `json:"complexity,omitempty"`
	Test       bool   `json:"test,omitempty"`
}
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"` // imports, declares, or calls_syntactic; NOT type-resolved.
}
type Finding struct {
	ID     string `json:"id"`
	Rule   string `json:"rule"`
	Symbol string `json:"symbol,omitempty"`
	Line   int    `json:"line"`
	Excess int    `json:"excess,omitempty"`
}
type File struct {
	Path       string    `json:"path"`
	Hash       string    `json:"hash"`
	Symbols    []Symbol  `json:"symbols,omitempty"`
	Edges      []Edge    `json:"edges,omitempty"`
	Findings   []Finding `json:"findings,omitempty"`
	Quality    []Finding `json:"quality,omitempty"`
	Generated  bool      `json:"generated,omitempty"`
	ParseError bool      `json:"parse_error,omitempty"`
}
type Graph struct {
	Version int              `json:"version"`
	Files   map[string]*File `json:"files"`
	Hash    string           `json:"hash"`
}
type ScanStats struct{ Read, Parsed, Reused, Removed int }

func Hash(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }

// AllowedPath is deliberately conservative. Hidden files, vendor trees, secrets,
// binaries and symlinks are not implicit agent context. User notes are separate.
func AllowedPath(path string) bool {
	if path == "" || filepath.IsAbs(path) || strings.Contains(path, "\\") {
		return false
	}
	for _, r := range path {
		if r < 32 || r == 127 {
			return false
		}
	}
	path = filepath.ToSlash(path)
	if filepath.ToSlash(filepath.Clean(path)) != path {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		lower := strings.ToLower(part)
		if part == "" || strings.HasPrefix(part, ".") || lower == "node_modules" || lower == "vendor" || lower == "secrets" || lower == "credentials" {
			return false
		}
	}
	name := strings.ToLower(filepath.Base(path))
	if strings.Contains(name, "secret") || strings.Contains(name, "credential") || name == "id_rsa" || name == "id_ed25519" {
		return false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".mod", ".sum", ".py", ".rs", ".js", ".jsx", ".ts", ".tsx", ".java", ".cs", ".c", ".h", ".cpp", ".rb", ".sql", ".md", ".txt", ".yaml", ".yml", ".toml", ".json", ".html", ".css", ".sh", ".tf":
		return true
	}
	return false
}

// ReadSource rejects symlinks at every path component. This is defence in depth,
// not a sandbox against a malicious process concurrently replacing directories.
var privateKeyHeader = regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH |DSA |ENCRYPTED )?PRIVATE KEY-----`)

func ReadSource(root, path string) ([]byte, error) {
	if !AllowedPath(path) {
		return nil, errors.New("path is outside the source allowlist")
	}
	current := root
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("symlink context is not allowed")
		}
	}
	f, err := os.Open(current)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxFileBytes {
		return nil, errors.New("source is not a regular file within the size limit")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileBytes || bytes.IndexByte(data, 0) >= 0 {
		return nil, errors.New("oversized or binary source")
	}
	if privateKeyHeader.Match(data) {
		return nil, errors.New("private key material is not allowed")
	}
	return data, nil
}

func expressionName(expr ast.Expr) string {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.SelectorExpr:
		return expressionName(n.X) + "." + n.Sel.Name
	case *ast.StarExpr:
		return expressionName(n.X)
	case *ast.IndexExpr:
		return expressionName(n.X)
	case *ast.IndexListExpr:
		return expressionName(n.X)
	case *ast.ParenExpr:
		return expressionName(n.X)
	}
	return ""
}

// Analyze reuses an unchanged immutable file node. It never retains the AST or
// file contents; persisted graph records contain identifiers and metrics only.
func Analyze(path string, data []byte, old *File) (*File, bool) {
	hash := Hash(data)
	if old != nil && old.Hash == hash {
		return old, false
	}
	result := &File{Path: path, Hash: hash}
	if filepath.Ext(path) != ".go" {
		return result, true
	}
	positions := token.NewFileSet()
	parsed, err := parser.ParseFile(positions, path, data, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		result.ParseError = true
		return result, true
	}
	result.Generated = ast.IsGenerated(parsed)
	for _, imp := range parsed.Imports {
		name, err := strconv.Unquote(imp.Path.Value)
		if err == nil {
			result.Edges = append(result.Edges, Edge{path, name, "imports"})
		}
	}
	add := func(name, kind string, node ast.Node, complexity int, test bool) string {
		id := path + "::" + kind + ":" + name
		result.Symbols = append(result.Symbols, Symbol{id, name, kind, positions.Position(node.Pos()).Line, positions.Position(node.End()).Line, complexity, test})
		result.Edges = append(result.Edges, Edge{path, id, "declares"})
		return id
	}
	for _, declaration := range parsed.Decls {
		switch n := declaration.(type) {
		case *ast.FuncDecl:
			name := n.Name.Name
			if n.Recv != nil && len(n.Recv.List) > 0 {
				name = expressionName(n.Recv.List[0].Type) + "." + name
			}
			complexity := 1
			var calls []string
			if n.Body != nil {
				ast.Inspect(n.Body, func(node ast.Node) bool {
					switch x := node.(type) {
					case *ast.FuncLit:
						return false // nested function has its own control flow
					case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt:
						complexity++
					case *ast.CaseClause:
						if x.List != nil {
							complexity++
						}
					case *ast.CommClause:
						if x.Comm != nil {
							complexity++
						}
					case *ast.BinaryExpr:
						if x.Op == token.LAND || x.Op == token.LOR {
							complexity++
						}
					case *ast.CallExpr:
						if target := expressionName(x.Fun); target != "" {
							calls = append(calls, target)
						}
					}
					return true
				})
			}
			test := strings.HasSuffix(path, "_test.go")
			id := add(name, "function", n, complexity, test)
			for _, target := range calls {
				result.Edges = append(result.Edges, Edge{id, target, "calls_syntactic"})
			}
			if !test && !result.Generated && complexity <= 10 {
				result.Quality = append(result.Quality, Finding{id + "::low_complexity", "low_complexity", id, positions.Position(n.Pos()).Line, 0})
			}
			if !test && !result.Generated && complexity > 10 {
				result.Findings = append(result.Findings, Finding{id + "::complexity", "complexity", id, positions.Position(n.Pos()).Line, complexity - 10})
			}
		case *ast.GenDecl:
			for _, spec := range n.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					add(s.Name.Name, "type", s, 0, false)
				case *ast.ValueSpec:
					for _, name := range s.Names {
						add(name.Name, n.Tok.String(), s, 0, false)
					}
				}
			}
		}
	}
	for _, group := range parsed.Comments {
		for _, comment := range group.List {
			text := strings.ToUpper(comment.Text)
			if strings.Contains(text, "TODO") || strings.Contains(text, "FIXME") {
				line := positions.Position(comment.Pos()).Line
				result.Findings = append(result.Findings, Finding{fmt.Sprintf("%s::note:%d", path, line), "debt_note", "", line, 0})
			}
		}
	}
	return result, true
}

// Scan hashes source content (not just timestamps) and only reparses changed
// files. Run it in a worker, on explicit refresh/run/check, never on a keypress.
// Returning a new map preserves the previous graph if scanning fails halfway.
func Scan(ctx context.Context, root string, previous *Graph) (*Graph, ScanStats, error) {
	result := &Graph{Version: SchemaVersion, Files: map[string]*File{}}
	var stats ScanStats
	var bytesRead int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			name := strings.ToLower(entry.Name())
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" || name == "secrets" || name == "credentials" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 && AllowedPath(rel) {
			return errors.New("source symlink excluded from graph; use a workspace without source symlinks")
		}
		if entry.Type()&os.ModeSymlink != 0 || !AllowedPath(rel) {
			return nil
		}
		if len(result.Files) >= MaxFiles {
			return errors.New("code graph file limit exceeded; narrow the workspace")
		}
		data, err := ReadSource(root, rel)
		if err != nil {
			return fmt.Errorf("index %s: %w", rel, err)
		}
		bytesRead += int64(len(data))
		if bytesRead > 128<<20 {
			return errors.New("code graph input limit exceeded; narrow the workspace")
		}
		var old *File
		if previous != nil && previous.Version == SchemaVersion {
			old = previous.Files[rel]
		}
		node, changed := Analyze(rel, data, old)
		result.Files[rel] = node
		stats.Read++
		if changed {
			stats.Parsed++
		} else {
			stats.Reused++
		}
		return nil
	})
	if err != nil {
		return nil, stats, err
	}
	if previous != nil {
		for path := range previous.Files {
			if _, ok := result.Files[path]; !ok {
				stats.Removed++
			}
		}
	}
	paths := make([]string, 0, len(result.Files))
	for path := range result.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	digest := sha256.New()
	for _, path := range paths {
		fmt.Fprintf(digest, "%s\x00%s\n", path, result.Files[path].Hash)
	}
	result.Hash = hex.EncodeToString(digest.Sum(nil))
	return result, stats, nil
}

type Store struct {
	Dir  string
	lock *os.File
}

func OpenStore(root, stateHome string) (*Store, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	if stateHome == "" {
		stateHome = os.Getenv("XDG_STATE_HOME")
		if stateHome == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, err
			}
			stateHome = filepath.Join(home, ".local", "state")
		}
	}
	if !filepath.IsAbs(stateHome) {
		return nil, errors.New("state home must be absolute")
	}
	dir := filepath.Join(stateHome, "code-editor", "agent", Hash([]byte(absolute)))
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(dir); err != nil || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("invalid state directory")
	}
	if err = os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("another KiwiCode agent owns this workspace state")
	}
	return &Store{dir, lock}, nil
}
func (s *Store) Close() error {
	if s.lock == nil {
		return nil
	}
	err := s.lock.Close()
	s.lock = nil
	return err
}
func storeName(name string) bool {
	return name == "session.json" || name == "graph.json" || name == "baseline.json"
}
func (s *Store) Load(name string, value any, limit int64) error {
	if !storeName(name) {
		return errors.New("invalid state name")
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, name), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return errors.New("state size limit exceeded")
	}
	return json.Unmarshal(data, value)
}
func (s *Store) Save(name string, value any, limit int) error {
	if !storeName(name) {
		return errors.New("invalid state name")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) > limit {
		return errors.New("state size limit exceeded")
	}
	f, err := os.CreateTemp(s.Dir, ".checkpoint-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), filepath.Join(s.Dir, name)); err != nil {
		return err
	}
	dir, err := os.Open(s.Dir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

type Evidence struct {
	Snapshot    string `json:"snapshot"`
	TestsPassed bool   `json:"tests_passed"`
	VetPassed   bool   `json:"vet_passed"`
	Reviewed    bool   `json:"reviewed"`
}
type CreditDelta struct {
	SymbolHash string `json:"symbol_hash"`
	Label      string `json:"label"`
	Points     int    `json:"points"`
}

type Reward struct {
	Baseline          string        `json:"baseline"`
	Policy            int           `json:"policy"`
	Components        []CreditDelta `json:"components,omitempty"`
	OmittedComponents int           `json:"omitted_components,omitempty"`
	Snapshot          string        `json:"snapshot"`
	Score             int           `json:"score"`
	NewCredit         int           `json:"new_credit"`
	Reason            string        `json:"reason"`
}

// Evaluate rewards verified reduction of complexity excess against a fixed
// baseline, not code volume, deleting TODOs, or an LLM's opinion. Score is
// cumulative, not an additive payout; high-water credit prevents fix/revert farms.
func Evaluate(base, current *Graph, evidence Evidence, highWater int) Reward {
	r := Reward{}
	if base == nil || current == nil {
		r.Reason = "a baseline and current graph are required"
		return r
	}
	r.Snapshot = current.Hash
	r.Baseline = base.Hash
	r.Policy = SchemaVersion
	if base.Version != SchemaVersion || current.Version != SchemaVersion {
		r.Reason = "unsupported graph policy"
		return r
	}
	if !evidence.Reviewed || !evidence.TestsPassed || !evidence.VetPassed || evidence.Snapshot != current.Hash {
		r.Reason = "reward requires review plus passing tests/vet on this exact snapshot"
		return r
	}
	originals := map[string]Symbol{}
	present := map[string]bool{}
	before, after := 0, 0
	deltas := map[string]int{}
	for _, f := range base.Files {
		if f.ParseError {
			r.Reason = "baseline has parse errors"
			return r
		}
		if f.Generated {
			continue
		}
		for _, sym := range f.Symbols {
			if sym.Kind == "function" {
				originals[sym.ID] = sym
			}
		}
		for _, finding := range f.Findings {
			if finding.Rule == "complexity" {
				before += finding.Excess
				deltas[finding.Symbol] += 5 * finding.Excess
			}
		}
	}
	for path, f := range current.Files {
		if f.ParseError {
			r.Reason = "candidate has parse errors"
			return r
		}
		// Test modifications need a richer evaluator, not automatic credit.
		if strings.HasSuffix(path, "_test.go") {
			if old, ok := base.Files[path]; ok && old.Hash != f.Hash {
				r.Reason = "existing tests changed; automatic reward withheld"
				return r
			}
		}
		if f.Generated {
			continue
		}
		for _, sym := range f.Symbols {
			present[sym.ID] = true
		}
		for _, finding := range f.Findings {
			if finding.Rule == "complexity" {
				after += finding.Excess
				deltas[finding.Symbol] -= 5 * finding.Excess
			}
		}
	}
	for path := range base.Files {
		if strings.HasSuffix(path, "_test.go") {
			if _, ok := current.Files[path]; !ok {
				r.Reason = "test deletion cannot earn reward"
				return r
			}
		}
	}
	for id := range originals {
		if !present[id] {
			r.Reason = "deleted/renamed functions require manual evaluation; no automatic reward"
			return r
		}
	}
	ids := make([]string, 0, len(deltas))
	for id, points := range deltas {
		if points != 0 {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		if len(r.Components) < 64 {
			r.Components = append(r.Components, CreditDelta{Hash([]byte(id)), clip(id, 256), deltas[id]})
		} else {
			r.OmittedComponents++
		}
	}
	r.Score = 5 * (before - after)
	r.NewCredit = max(0, r.Score-highWater)
	r.Reason = "verified baseline complexity-debt delta (policy v1); not proof of overall quality"
	return r
}
