package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const rolesCompletionSource = "public sealed class ListRolesHandler {\n private readonly IMembershipService _members;\n public async Task HandleAsync() {\n  var members = await _members.ListMembersAsync(ct);\n  var counts = members\n    .Gr();\n }\n}\n"
const membershipCompletionSource = "public interface IMembershipService {\n Task<IReadOnlyList<MemberView>> ListMembersAsync(CancellationToken ct);\n}\n"

func TestCSharpAwaitedCollectionCompletion(t *testing.T) {
	root := t.TempDir()
	app, port := filepath.Join(root, "ListRolesHandler.cs"), filepath.Join(root, "IMembershipService.cs")
	if err := os.WriteFile(port, []byte(membershipCompletionSource), 0600); err != nil {
		t.Fatal(err)
	}
	completion := dependencyCompletions(app, []string{port}, map[string][]byte{app: []byte(rolesCompletionSource)})
	b := newBuffer(app, []byte(rolesCompletionSource))
	b.row, b.col = 5, 7 // .Gr|() in a continued expression, not at end-of-line.
	if got := string(dependencySuggestion(b, completion)); got != "oupBy" {
		t.Fatalf("awaited collection completion = %q; receivers=%v", got, completion.receivers)
	}
	if names := dependencyCandidates(b, completion); !slices.Contains(names, "GroupBy") || !slices.Contains(names, "GroupJoin") {
		t.Fatalf("missing LINQ options: %v", names)
	}
	b.lines[5] = []rune("    .")
	b.col = 5
	names := dependencyCandidates(b, completion)
	for _, want := range []string{"Count", "Where", "Select", "GroupBy", "ToDictionary", "ToList"} {
		if !slices.Contains(names, want) {
			t.Fatalf("dot did not populate %s", want)
		}
	}
	b.lines[5] = []rune("    .Gr();")
	b.col = 7
	if allocations := testing.AllocsPerRun(100, func() { dependencyCandidates(b, completion) }); allocations > 12 {
		t.Fatalf("warm member lookup allocated %.0f times; it must not reparse the buffer", allocations)
	}
	e := &editor{buffers: []*buffer{b}, completionCache: map[string]dependencyCompletion{app: completion}}
	e.handle(key{code: keyTab})
	if string(b.lines[5]) != "    .GroupBy();" {
		t.Fatalf("Tab failed to complete in-place: %q", string(b.lines[5]))
	}
	for _, source := range []string{
		"IReadOnlyList<MemberView> members;\nmembers.Gr",
		"List<MemberView> members;\nmembers?.Gr",
		"MemberView[] members;\nmembers.Gr",
	} {
		c := dependencyCompletions(app, nil, map[string][]byte{app: []byte(source)})
		b := newBuffer(app, []byte(source))
		b.row = 1
		b.col = len(b.lines[1])
		if got := string(dependencySuggestion(b, c)); got != "oupBy" {
			t.Fatalf("%q completion = %q", source, got)
		}
	}
	source := "public class MemberView {}\nMemberView member;\nmember.Gr"
	c := dependencyCompletions(app, nil, map[string][]byte{app: []byte(source)})
	b = newBuffer(app, []byte(source))
	b.row = 2
	b.col = len(b.lines[2])
	if slices.Contains(dependencyCandidates(b, c), "GroupBy") {
		t.Fatal("collection methods leaked onto a scalar")
	}
}

func BenchmarkCSharpMemberLookup(b *testing.B) {
	source := strings.Repeat("// unrelated source line\n", 2000) + rolesCompletionSource
	returns := map[string]string{}
	indexCSharpReturns(returns, []byte(membershipCompletionSource))
	c := dependencyCompletion{receivers: csharpReceiverTypes("ListRolesHandler.cs", []byte(source), returns), members: map[string][]string{"IReadOnlyList": enumerableMembers}}
	buffer := newBuffer("ListRolesHandler.cs", []byte(source))
	buffer.row, buffer.col = 2005, 7
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dependencyCandidates(buffer, c)
	}
}
