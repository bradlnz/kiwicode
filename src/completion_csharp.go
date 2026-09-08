package main

import (
	"regexp"
	"strings"
)

const csharpTypePattern = `[A-Za-z_][\w.]*(?:\s*<[^;={}()\r\n]+>)?(?:\[\])?\??`

var csharpMethodReturn = regexp.MustCompile(`\b(` + csharpTypePattern + `)\s+([A-Za-z_]\w*)\s*\(`)
var csharpDeclaration = regexp.MustCompile(`\b(` + csharpTypePattern + `)\s+([A-Za-z_]\w*)\s*[;=,){]`)
var csharpAwaitAssignment = regexp.MustCompile(`\bvar\s+([A-Za-z_]\w*)\s*=\s*(await\s+)?([A-Za-z_]\w*)\s*\.\s*([A-Za-z_]\w*)\s*\(`)
var completionReceiver = regexp.MustCompile(`([A-Za-z_$][\w$]*)\s*(?:\?\.|\.|->)`)

// ponytail: bounded syntax inference, not Roslyn. Overload resolution, complex
// expression chains and exact extension-method scope need a language server.
func indexCSharpReturns(returns map[string]string, data []byte) {
	types := modelTypePatterns["dotnet"].FindAllSubmatchIndex(data, -1)
	for _, method := range csharpMethodReturn.FindAllSubmatchIndex(data, -1) {
		owner := ""
		for _, decl := range types {
			if decl[0] > method[0] {
				break
			}
			owner = string(data[decl[2]:decl[3]])
		}
		if owner == "" {
			continue
		}
		key := owner + "." + string(data[method[4]:method[5]])
		value := strings.ReplaceAll(string(data[method[2]:method[3]]), " ", "")
		if previous, ok := returns[key]; ok && previous != value {
			value = ""
		} // Ambiguous overloads stay unknown.
		returns[key] = value
	}
}

func csharpReceiverTypes(path string, data []byte, returns map[string]string) map[string]string {
	types := map[string]string{}
	for _, decl := range csharpDeclaration.FindAllSubmatch(data, -1) {
		if string(decl[1]) != "var" {
			types[string(decl[2])] = strings.ReplaceAll(string(decl[1]), " ", "")
		}
	}
	for _, assignment := range csharpAwaitAssignment.FindAllSubmatch(data, -1) {
		owner := baseType(types[string(assignment[3])])
		returnType := returns[owner+"."+string(assignment[4])]
		if returnType == "" {
			continue
		}
		if len(assignment[2]) > 0 {
			if name := baseType(returnType); name == "Task" || name == "ValueTask" {
				start, end := strings.IndexByte(returnType, '<'), strings.LastIndexByte(returnType, '>')
				if start < 0 || end <= start {
					continue
				}
				returnType = returnType[start+1 : end]
			} else {
				continue
			}
		}
		types[string(assignment[1])] = returnType
	}
	b := newBuffer(path, data)
	for _, access := range completionReceiver.FindAllSubmatch(data, -1) {
		receiver := string(access[1])
		if _, known := types[receiver]; !known {
			types[receiver] = objectType(b, receiver)
		}
	}
	for receiver, owner := range types {
		if strings.HasSuffix(strings.TrimSuffix(owner, "?"), "[]") {
			types[receiver] = "Array"
		} else {
			types[receiver] = baseType(owner)
		}
	}
	return types
}

var enumerableMembers = strings.Fields("Aggregate All Any Append AsEnumerable Average Cast Chunk Concat Contains Count DefaultIfEmpty Distinct DistinctBy ElementAt ElementAtOrDefault Except ExceptBy First FirstOrDefault GroupBy GroupJoin Intersect IntersectBy Join Last LastOrDefault LongCount Max MaxBy Min MinBy OfType OrderBy OrderByDescending Prepend Reverse Select SelectMany SequenceEqual Single SingleOrDefault Skip SkipLast SkipWhile Sum Take TakeLast TakeWhile ToArray ToDictionary ToHashSet ToList ToLookup Union UnionBy Where Zip")

func addCollectionMembers(members map[string]map[string]bool, receivers map[string]string) {
	for _, owner := range receivers {
		extra := ""
		switch owner {
		case "IEnumerable", "IQueryable", "IOrderedEnumerable", "IOrderedQueryable":
			extra = "GetEnumerator"
		case "IReadOnlyList", "IReadOnlyCollection":
			extra = "Count GetEnumerator"
		case "IList", "ICollection":
			extra = "Count IsReadOnly Add Clear Contains CopyTo Remove GetEnumerator"
		case "List":
			extra = "Count Capacity Add AddRange Clear Contains ConvertAll CopyTo Exists Find FindAll FindIndex FindLast FindLastIndex ForEach GetEnumerator GetRange IndexOf Insert InsertRange LastIndexOf Remove RemoveAll RemoveAt RemoveRange Reverse Sort ToArray TrimExcess TrueForAll"
		case "Array":
			extra = "Length LongLength Rank GetLength GetLongLength GetLowerBound GetUpperBound GetValue SetValue Clone CopyTo GetEnumerator"
		case "HashSet", "ISet":
			extra = "Count Add Clear Contains CopyTo ExceptWith IntersectWith IsProperSubsetOf IsProperSupersetOf IsSubsetOf IsSupersetOf Overlaps Remove SetEquals SymmetricExceptWith UnionWith GetEnumerator"
		default:
			continue
		}
		if members[owner] == nil {
			members[owner] = map[string]bool{}
		}
		for _, name := range enumerableMembers {
			members[owner][name] = true
		}
		for _, name := range strings.Fields(extra) {
			members[owner][name] = true
		}
	}
}
