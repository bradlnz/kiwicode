package main

import (
	"path/filepath"
	"strings"
	"unicode"
)

type languageSyntax struct {
	comment  string
	keywords map[string]bool
}

var languageSyntaxes = map[string]languageSyntax{
	".go":     {"//", wordSet("break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var true false nil")},
	".js":     {"//", wordSet("async await break case catch class const continue default delete do else export extends false finally for from function if import in instanceof let new null of return static super switch this throw true try typeof undefined var void while yield")},
	".jsx":    {"//", wordSet("async await break case catch class const continue default else export extends false for from function if import let new null return static super switch this throw true try typeof undefined var while yield")},
	".ts":     {"//", wordSet("abstract any as async await boolean break case catch class const continue default else enum export extends false finally for from function if implements import interface keyof let namespace never new null number private protected public readonly return static string super switch this throw true try type typeof undefined unknown var void while yield")},
	".tsx":    {"//", wordSet("abstract any as async await boolean break case catch class const continue default else enum export extends false for from function if implements import interface let namespace never new null number private protected public readonly return static string super switch this throw true try type typeof undefined unknown var void while yield")},
	".py":     {"#", wordSet("and as assert async await break class continue def del elif else except False finally for from global if import in is lambda None nonlocal not or pass raise return True try while with yield")},
	".rs":     {"//", wordSet("as async await break const continue crate dyn else enum extern false fn for if impl in let loop match mod move mut pub ref return self Self static struct super trait true type unsafe use where while")},
	".c":      {"//", wordSet("auto break case char const continue default do double else enum extern float for goto if inline int long register restrict return short signed sizeof static struct switch typedef union unsigned void volatile while")},
	".h":      {"//", wordSet("auto break case char const continue default do double else enum extern float for if inline int long return short signed sizeof static struct switch typedef union unsigned void volatile while")},
	".cpp":    {"//", wordSet("alignas auto bool break case catch char class const constexpr continue default delete do double else enum explicit export extern false float for friend if inline int long namespace new nullptr operator private protected public return short signed sizeof static struct switch template this throw true try typedef typename union unsigned using virtual void volatile while")},
	".hpp":    {"//", wordSet("auto bool break case catch char class const constexpr continue default delete do double else enum explicit extern false float for friend if inline int long namespace new nullptr operator private protected public return short signed sizeof static struct switch template this throw true try typename unsigned using virtual void while")},
	".java":   {"//", wordSet("abstract assert boolean break byte case catch char class const continue default do double else enum extends false final finally float for if implements import instanceof int interface long native new null package private protected public return short static strictfp super switch synchronized this throw throws transient true try void volatile while")},
	".sh":     {"#", wordSet("case do done elif else esac fi for function if in select then time until while")},
	".bash":   {"#", wordSet("case do done elif else esac fi for function if in select then time until while")},
	".zsh":    {"#", wordSet("case do done elif else esac fi for function if in select then time until while")},
	".json":   {"", wordSet("true false null")},
	".sql":    {"--", wordSet("alter and as asc between by case create delete desc distinct drop else end exists from group having in index insert into is join like limit not null on or order outer primary select set table then union unique update values when where")},
	".yaml":   {"#", wordSet("true false null yes no on off")},
	".yml":    {"#", wordSet("true false null yes no on off")},
	".tf":     {"#", wordSet("data dynamic for for_each if in locals module output provider resource terraform true false null variable")},
	".tfvars": {"#", wordSet("true false null")},
	".hcl":    {"#", wordSet("data dynamic for for_each if in locals module output provider resource terraform true false null variable")},
	".cs":     {"//", wordSet("abstract as async await base bool break byte case catch char checked class const continue decimal default delegate do double else enum event explicit extern false finally fixed float for foreach goto if implicit in int interface internal is lock long namespace new null object operator out override params private protected public readonly record ref return sbyte sealed short sizeof stackalloc static string struct switch this throw true try typeof uint ulong unchecked unsafe ushort using var virtual void volatile while yield")},
	".fs":     {"//", wordSet("abstract and as assert base begin class default delegate do done downcast downto elif else end exception extern false finally fixed for fun function global if in inherit inline interface internal lazy let match member module mutable namespace new not null of open or override private public rec return static struct then to true try type upcast use val void when while with yield")},
	".fsx":    {"//", wordSet("and as assert do elif else false for fun function if in let match module mutable namespace new null of open rec return then true try type use when while with yield")},
	".vb":     {"'", wordSet("addhandler addressOf alias and andalso as boolean byref byte byval call case catch class const continue date decimal default delegate dim do double each else elseif end enum event exit false finally for friend function get global goto handles if implements imports in inherits integer interface is let lib long loop me module mustinherit mustoverride mybase namespace new next not nothing object of on operator option optional or orelse out overrides overloads private property protected public raiseevent readonly redim rem removehandler resume return select set shadows shared short single static step stop string structure sub synclock then throw to true try typeof variant wend while with writeonly xor")},
	".razor":  {"//", wordSet("as async await class else false for foreach if in model namespace new null private protected public return true using while")},
	".cshtml": {"//", wordSet("as async await class else false for foreach if in model namespace new null private protected public return true using while")},
	".rb":     {"#", wordSet("alias and begin break case class def defined do else elsif end ensure false for if in module next nil not or redo rescue retry return self super then true undef unless until when while yield")},
	".php":    {"//", wordSet("abstract and array as break callable case catch class clone const continue declare default do echo else elseif empty enddeclare endfor endforeach endif endswitch endwhile eval exit extends final finally fn for foreach function global goto if implements include include_once instanceof insteadof interface isset list match namespace new null or print private protected public readonly require require_once return static switch throw trait true try unset use var while xor yield")},
	".kt":     {"//", wordSet("as break class continue do else false for fun if in interface is null object package return super this throw true try typealias val var when while")},
	".swift":  {"//", wordSet("as associatedtype break case catch class continue default defer deinit do else enum extension fallthrough false fileprivate for func guard if import in init inout internal is let nil open operator private protocol public repeat rethrows return self static struct subscript super switch throw throws true try typealias var where while")},
	".css":    {"/*", wordSet("important inherit initial none auto block inline flex grid relative absolute fixed sticky")},
	".scss":   {"//", wordSet("and as at-root content debug each else error extend false for forward from function if import in include mixin not null or return true use warn while")},
}

var markupExtensions = map[string]bool{".html": true, ".htm": true, ".xml": true, ".csproj": true, ".fsproj": true, ".vbproj": true, ".svg": true}

var railsDSL = wordSet("add_column after_action before_action belongs_to collection create_table delete desc destroy devise_scope draw enum gem get group has_and_belongs_to_many has_many has_one helper_method index layout link_to member namespace patch post put redirect_to render require require_relative resources resource root scope source task validates")

var builtinTypes = map[string]map[string]bool{
	".go":   wordSet("any bool byte comparable complex64 complex128 error float32 float64 int int8 int16 int32 int64 rune string uint uint8 uint16 uint32 uint64 uintptr"),
	".js":   wordSet("Array BigInt Boolean Date Error Map Number Object Promise RegExp Set String Symbol"),
	".jsx":  wordSet("Array BigInt Boolean Date Error Map Number Object Promise RegExp Set String Symbol"),
	".ts":   wordSet("any bigint boolean never number object string symbol unknown void"),
	".tsx":  wordSet("any bigint boolean never number object string symbol unknown void"),
	".py":   wordSet("bool bytes bytearray complex dict float frozenset int list memoryview object range set str tuple type"),
	".rs":   wordSet("bool char f32 f64 i8 i16 i32 i64 i128 isize str u8 u16 u32 u64 u128 usize String Vec Option Result"),
	".cs":   wordSet("bool byte char decimal double dynamic float int long object sbyte short string uint ulong ushort void Boolean Byte Char DateOnly DateTime DateTimeOffset Decimal Double Guid Int16 Int32 Int64 Object SByte Single String TimeOnly TimeSpan UInt16 UInt32 UInt64 Uri"),
	".java": wordSet("boolean byte char double float int long short void String Object"),
}

func wordSet(words string) map[string]bool {
	set := make(map[string]bool)
	for _, word := range strings.Fields(words) {
		set[word] = true
		set[strings.ToLower(word)] = true
	}
	return set
}

func isMarkupDocument(path string, lines [][]rune) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if markupExtensions[ext] {
		return true
	}
	if ext == ".erb" || ext == ".md" || railsRubyFile(path) || languageSyntaxes[ext].keywords != nil {
		return false
	}
	// ponytail: 64 KiB keeps detection cheap; raise it only for real XML with unusually large tag-free headers.
	var sample strings.Builder
	for _, line := range lines {
		if sample.Len()+len(line) > 64<<10 {
			break
		}
		sample.WriteString(string(line))
		sample.WriteByte('\n')
	}
	return hasMarkupStructure(sample.String())
}

func hasMarkupStructure(text string) bool {
	opened := map[string]bool{}
	for cursor := 0; cursor < len(text); {
		relative := strings.IndexByte(text[cursor:], '<')
		if relative < 0 {
			return false
		}
		start := cursor + relative
		switch {
		case strings.HasPrefix(text[start:], "<!--"):
			end := strings.Index(text[start+4:], "-->")
			if end < 0 {
				return false
			}
			cursor = start + 4 + end + 3
			continue
		case strings.HasPrefix(text[start:], "<![CDATA["):
			end := strings.Index(text[start+9:], "]]>")
			if end < 0 {
				return false
			}
			cursor = start + 9 + end + 3
			continue
		case strings.HasPrefix(text[start:], "<?"):
			end := strings.Index(text[start+2:], "?>")
			if end < 0 {
				return false
			}
			cursor = start + 2 + end + 2
			continue
		case strings.HasPrefix(text[start:], "<!"):
			cursor = start + 2
			continue
		}
		nameStart := start + 1
		closing := nameStart < len(text) && text[nameStart] == '/'
		if closing {
			nameStart++
		}
		nameEnd := nameStart
		for nameEnd < len(text) && markupNameRune(rune(text[nameEnd])) {
			nameEnd++
		}
		if nameEnd == nameStart || nameStart >= len(text) || !unicode.IsLetter(rune(text[nameStart])) && text[nameStart] != '_' {
			cursor = start + 1
			continue
		}
		tagEnd := strings.IndexByte(text[nameEnd:], '>')
		if tagEnd < 0 {
			return false
		}
		tagEnd += nameEnd
		name := text[nameStart:nameEnd]
		if strings.HasSuffix(strings.TrimSpace(text[nameEnd:tagEnd]), "/") {
			return true
		}
		if closing {
			if opened[name] {
				return true
			}
		} else {
			opened[name] = true
		}
		cursor = tagEnd + 1
	}
	return false
}

func highlightLine(path string, line []rune) string {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".erb" {
		return highlightERB(line)
	}
	if railsRubyFile(path) {
		ext = ".rb"
	}
	if markupExtensions[ext] {
		return highlightMarkup(line)
	}
	if ext == ".md" && strings.HasPrefix(strings.TrimSpace(string(line)), "#") {
		return paint(colors.function, string(line))
	}
	syntax, ok := languageSyntaxes[ext]
	if !ok {
		return string(line)
	}

	parameters := parameterStarts(ext, line, syntax)
	types := typeStarts(ext, line)
	names := csharpNameStarts(ext, line)
	var out strings.Builder
	for i := 0; i < len(line); {
		if hasRunesAt(line, i, "/*") {
			end := i + 2
			for end < len(line) && !hasRunesAt(line, end, "*/") {
				end++
			}
			end = min(len(line), end+2)
			out.WriteString(paint(colors.comment, string(line[i:end])))
			i = end
			continue
		}
		if syntax.comment != "" && hasRunesAt(line, i, syntax.comment) {
			out.WriteString(paint(colors.comment, string(line[i:])))
			break
		}
		if strings.ContainsRune("'\"`", line[i]) {
			quote := line[i]
			start := i
			i++
			for i < len(line) {
				if line[i] == '\\' {
					i += min(2, len(line)-i)
				} else {
					i++
					if line[i-1] == quote {
						break
					}
				}
			}
			out.WriteString(paint(colors.stringValue, string(line[start:i])))
			continue
		}
		if ext == ".rb" && (line[i] == '@' || line[i] == '$' || line[i] == ':' && i+1 < len(line) && identifierRune(line[i+1])) {
			start := i
			i++
			if line[start] == '@' && i < len(line) && line[i] == '@' {
				i++
			}
			for i < len(line) && (identifierRune(line[i]) || strings.ContainsRune("!?", line[i])) {
				i++
			}
			color := colors.parameter
			if line[start] == ':' {
				color = colors.stringValue
			}
			out.WriteString(paint(color, string(line[start:i])))
			continue
		}
		if unicode.IsDigit(line[i]) {
			start := i
			for i < len(line) && (unicode.IsDigit(line[i]) || strings.ContainsRune("._xabcdefABCDEF", line[i])) {
				i++
			}
			out.WriteString(paint(colors.number, string(line[start:i])))
			continue
		}
		if unicode.IsLetter(line[i]) || line[i] == '_' {
			start := i
			for i < len(line) && (unicode.IsLetter(line[i]) || unicode.IsDigit(line[i]) || line[i] == '_') {
				i++
			}
			word := string(line[start:i])
			if types[start] || builtinTypes[ext][word] || builtinTypes[ext][strings.ToLower(word)] {
				out.WriteString(paint(colors.typeName, word))
			} else if syntax.keywords[word] || syntax.keywords[strings.ToLower(word)] {
				out.WriteString(paint(colors.keyword, word))
			} else if parameters[start] {
				out.WriteString(paint(colors.parameter, word))
			} else if ext == ".rb" && (railsDSL[word] || rubyDefinitionName(line, start, i)) {
				out.WriteString(paint(colors.function, word))
			} else if isStructuredKey(ext, line, i) {
				out.WriteString(paint(colors.function, word))
			} else if nextRune(line, i) == '(' {
				out.WriteString(paint(colors.function, word))
			} else if names[start] {
				out.WriteString(word)
			} else if isUpperIdentifier(word) {
				out.WriteString(paint(colors.typeName, word))
			} else {
				out.WriteString(word)
			}
			continue
		}
		if strings.ContainsRune("+-*/%=!<>|&^~?:", line[i]) {
			out.WriteString(paint(colors.operator, string(line[i])))
			i++
			continue
		}
		out.WriteRune(line[i])
		i++
	}
	return out.String()
}

func railsRubyFile(path string) bool {
	switch strings.ToLower(filepath.Base(path)) {
	case "gemfile", "rakefile", "guardfile", "capfile", "config.ru":
		return true
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".rake", ".gemspec", ".ru":
		return true
	}
	return false
}

func rubyDefinitionName(line []rune, start, end int) bool {
	prefix := strings.TrimSpace(string(line[:start]))
	return prefix == "def" || strings.HasPrefix(prefix, "def ") && strings.HasSuffix(prefix, ".")
}

func csharpNameStarts(ext string, line []rune) map[int]bool {
	result := map[int]bool{}
	if ext != ".cs" {
		return result
	}
	words := identifierPositions(line, 0, len(line))
	for index, word := range words {
		before := word.start - 1
		for before >= 0 && unicode.IsSpace(line[before]) {
			before--
		}
		previous := ""
		if index > 0 {
			previous = words[index-1].word
		}
		declaresType := strings.Contains(" class struct interface enum record delegate new typeof default ", " "+previous+" ")
		after := word.start + len([]rune(word.word))
		if before >= 0 && line[before] == '.' || !declaresType && strings.ContainsRune("{=;,", nextRune(line, after)) {
			result[word.start] = true
		}
	}
	return result
}

func paint(color int, text string) string { return ansiFG(color) + text + ansiFG(colors.text) }

func typeStarts(ext string, line []rune) map[int]bool {
	result := map[int]bool{}
	if ext != ".go" {
		return result
	}
	words := identifierPositions(line, 0, len(line))
	funcEnd := -1
	for _, word := range words {
		if word.word == "func" {
			funcEnd = word.start + len([]rune(word.word))
			break
		}
	}
	if funcEnd < 0 {
		return result
	}
	open := nextParen(line, funcEnd)
	if open < 0 {
		return result
	}
	if strings.TrimSpace(string(line[funcEnd:open])) == "" { // method receiver
		close := matchingParen(line, open)
		markGoTypeList(result, line, open+1, close, true)
		open = nextParen(line, close+1)
	}
	close := matchingParen(line, open)
	if open < 0 || close < 0 {
		return result
	}
	markGoTypeList(result, line, open+1, close, true)
	end := len(line)
	for i := close + 1; i < len(line); i++ {
		if line[i] == '{' {
			end = i
			break
		}
	}
	markGoTypeList(result, line, close+1, end, false)
	return result
}

func markGoTypeList(result map[int]bool, line []rune, start, end int, named bool) {
	if start < 0 || end < start {
		return
	}
	for start <= end {
		segmentEnd := start
		for segmentEnd < end && line[segmentEnd] != ',' {
			segmentEnd++
		}
		words := identifierPositions(line, start, segmentEnd)
		first := 0
		if named && len(words) > 1 {
			first = 1
		}
		for _, word := range words[first:] {
			if word.word != "func" && word.word != "chan" && word.word != "map" {
				result[word.start] = true
			}
		}
		start = segmentEnd + 1
	}
}

func nextParen(line []rune, start int) int {
	for i := max(0, start); i < len(line); i++ {
		if line[i] == '(' {
			return i
		}
	}
	return -1
}

// parameterStarts deliberately colors declarations only; language servers can add semantic uses later.
func parameterStarts(ext string, line []rune, syntax languageSyntax) map[int]bool {
	result := map[int]bool{}
	open := -1
	for i, value := range line {
		if value == '(' {
			open = i
			break
		}
	}
	if open < 0 {
		return result
	}
	prefix := strings.TrimSpace(string(line[:open]))
	declaration := strings.Contains(prefix, "func ") || strings.Contains(prefix, "def ") || strings.Contains(prefix, "fn ") ||
		strings.Contains(prefix, "function ") || strings.Contains(prefix, "fun ") || strings.Contains(prefix, " sub ")
	if ext == ".go" && strings.HasSuffix(prefix, "func") { // method receiver
		if close := matchingParen(line, open); close >= 0 {
			next := -1
			for i := close + 1; i < len(line); i++ {
				if line[i] == '(' {
					next = i
					break
				}
			}
			if next >= 0 {
				open = next
				declaration = true
			}
		}
	}
	close := matchingParen(line, open)
	if close < 0 {
		return result
	}
	tail := strings.TrimSpace(string(line[close+1:]))
	if !declaration && (strings.HasPrefix(tail, "{") || strings.HasPrefix(tail, "=>") || tail == ";") {
		first := strings.Fields(prefix)
		declaration = len(first) > 0 && !syntax.keywords[first[len(first)-1]]
	}
	if !declaration {
		return result
	}
	cLike := ext == ".c" || ext == ".h" || ext == ".cpp" || ext == ".hpp" || ext == ".cs" || ext == ".java"
	segmentStart := open + 1
	for segmentStart <= close {
		segmentEnd := segmentStart
		for segmentEnd < close && line[segmentEnd] != ',' {
			segmentEnd++
		}
		if cLike {
			for i := segmentStart; i < segmentEnd; i++ {
				if line[i] == '=' {
					segmentEnd = i
					break
				}
			}
		}
		words := identifierPositions(line, segmentStart, segmentEnd)
		if len(words) > 0 {
			pick := 0
			if cLike {
				pick = len(words) - 1
				for pick > 0 && syntax.keywords[words[pick].word] {
					pick--
				}
			}
			if !syntax.keywords[words[pick].word] {
				result[words[pick].start] = true
			}
		}
		segmentStart = segmentEnd + 1
	}
	return result
}

type positionedWord struct {
	word  string
	start int
}

func identifierPositions(line []rune, start, end int) []positionedWord {
	var words []positionedWord
	for i := start; i < end; {
		if unicode.IsLetter(line[i]) || line[i] == '_' {
			from := i
			for i < end && (unicode.IsLetter(line[i]) || unicode.IsDigit(line[i]) || line[i] == '_') {
				i++
			}
			words = append(words, positionedWord{string(line[from:i]), from})
		} else {
			i++
		}
	}
	return words
}

func matchingParen(line []rune, open int) int {
	if open < 0 || open >= len(line) || line[open] != '(' {
		return -1
	}
	depth := 0
	for i := open; i < len(line); i++ {
		switch line[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func isStructuredKey(ext string, line []rune, after int) bool {
	for after < len(line) && unicode.IsSpace(line[after]) {
		after++
	}
	return after < len(line) && (ext == ".yaml" || ext == ".yml" || ext == ".css" || ext == ".scss") && line[after] == ':' ||
		after < len(line) && ext == ".rb" && line[after] == ':' ||
		after < len(line) && (ext == ".tf" || ext == ".tfvars" || ext == ".hcl") && line[after] == '='
}

func nextRune(line []rune, index int) rune {
	for index < len(line) && unicode.IsSpace(line[index]) {
		index++
	}
	if index < len(line) {
		return line[index]
	}
	return 0
}

func isUpperIdentifier(word string) bool {
	runes := []rune(word)
	return len(runes) > 0 && unicode.IsUpper(runes[0])
}

func highlightMarkup(line []rune) string {
	var out strings.Builder
	for i := 0; i < len(line); {
		if hasRunesAt(line, i, "<!--") {
			end := i + 4
			for end < len(line) && !hasRunesAt(line, end, "-->") {
				end++
			}
			end = min(len(line), end+3)
			out.WriteString(paint(colors.comment, string(line[i:end])))
			i = end
		} else if line[i] == '<' {
			out.WriteString(paint(colors.operator, "<"))
			i++
			for i < len(line) && strings.ContainsRune("/!?", line[i]) {
				out.WriteString(paint(colors.operator, string(line[i])))
				i++
			}
			for i < len(line) && unicode.IsSpace(line[i]) {
				out.WriteRune(line[i])
				i++
			}
			start := i
			for i < len(line) && markupNameRune(line[i]) {
				i++
			}
			out.WriteString(paint(colors.function, string(line[start:i])))
			for i < len(line) && line[i] != '>' {
				if unicode.IsSpace(line[i]) {
					out.WriteRune(line[i])
					i++
					continue
				}
				if strings.ContainsRune("/?", line[i]) {
					out.WriteString(paint(colors.operator, string(line[i])))
					i++
					continue
				}
				start = i
				for i < len(line) && markupNameRune(line[i]) {
					i++
				}
				if start == i {
					out.WriteRune(line[i])
					i++
					continue
				}
				out.WriteString(paint(colors.parameter, string(line[start:i])))
				for i < len(line) && unicode.IsSpace(line[i]) {
					out.WriteRune(line[i])
					i++
				}
				if i >= len(line) || line[i] != '=' {
					continue
				}
				out.WriteString(paint(colors.operator, "="))
				i++
				for i < len(line) && unicode.IsSpace(line[i]) {
					out.WriteRune(line[i])
					i++
				}
				start = i
				if i < len(line) && strings.ContainsRune("'\"", line[i]) {
					quote := line[i]
					i++
					for i < len(line) && line[i] != quote {
						i++
					}
					i = min(len(line), i+1)
				} else {
					for i < len(line) && !unicode.IsSpace(line[i]) && !strings.ContainsRune("/?>", line[i]) {
						i++
					}
				}
				out.WriteString(paint(colors.stringValue, string(line[start:i])))
			}
			if i < len(line) {
				out.WriteString(paint(colors.operator, ">"))
				i++
			}
		} else {
			out.WriteRune(line[i])
			i++
		}
	}
	return out.String()
}

func highlightERB(line []rune) string {
	var out strings.Builder
	for i := 0; i < len(line); {
		open := i
		for open < len(line) && !hasRunesAt(line, open, "<%") {
			open++
		}
		if open > i {
			out.WriteString(highlightMarkup(line[i:open]))
		}
		if open == len(line) {
			break
		}
		close := open + 2
		for close < len(line) && !hasRunesAt(line, close, "%>") {
			close++
		}
		end := min(len(line), close+2)
		if open+2 < len(line) && line[open+2] == '#' {
			out.WriteString(paint(colors.comment, string(line[open:end])))
			i = end
			continue
		}
		codeStart := open + 2
		if codeStart < len(line) && strings.ContainsRune("=-", line[codeStart]) {
			codeStart++
		}
		out.WriteString(paint(colors.operator, string(line[open:codeStart])))
		codeEnd := close
		if codeEnd > codeStart && line[codeEnd-1] == '-' {
			codeEnd--
		}
		out.WriteString(highlightLine("template.rb", line[codeStart:codeEnd]))
		out.WriteString(paint(colors.operator, string(line[codeEnd:end])))
		i = end
	}
	return out.String()
}

func csharpRawHighlights(path string, lines [][]rune, first, last int) map[int]string {
	if !strings.EqualFold(filepath.Ext(path), ".cs") {
		return nil
	}
	inRaw := false
	for row := 0; row < min(first, len(lines)); row++ {
		if strings.Count(string(lines[row]), `"""`)%2 == 1 {
			inRaw = !inRaw
		}
	}
	highlights := map[int]string{}
	for row := max(0, first); row < min(last, len(lines)); row++ {
		line := expandLine(lines[row])
		if inRaw || strings.Contains(string(line), `"""`) {
			highlights[row], inRaw = highlightCSharpRawLine(line, inRaw)
		}
	}
	return highlights
}

func highlightCSharpRawLine(line []rune, inRaw bool) (string, bool) {
	var out strings.Builder
	for i := 0; i < len(line); {
		quote := i
		for quote < len(line) && !hasRunesAt(line, quote, `"""`) {
			quote++
		}
		if quote == len(line) {
			if inRaw {
				out.WriteString(paint(colors.stringValue, string(line[i:])))
			} else {
				out.WriteString(highlightLine("raw.cs", line[i:]))
			}
			break
		}
		if inRaw {
			out.WriteString(paint(colors.stringValue, string(line[i:quote+3])))
			i, inRaw = quote+3, false
			continue
		}
		marker := quote
		for marker > i && line[marker-1] == '$' {
			marker--
		}
		out.WriteString(highlightLine("raw.cs", line[i:marker]))
		out.WriteString(paint(colors.stringValue, string(line[marker:quote+3])))
		i, inRaw = quote+3, true
	}
	return out.String(), inRaw
}

func markupNameRune(value rune) bool {
	return unicode.IsLetter(value) || unicode.IsDigit(value) || strings.ContainsRune("_:-.", value)
}

func hasRunesAt(line []rune, index int, text string) bool {
	match := []rune(text)
	return index+len(match) <= len(line) && string(line[index:index+len(match)]) == text
}
