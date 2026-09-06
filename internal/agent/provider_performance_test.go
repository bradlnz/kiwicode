package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Fragmented arguments reproduce realistic large edits. Fixture generation is
// outside the timed region; both implementations consume identical SSE bytes.
func fragmentedToolStream(size int) (string, string) {
	args, _ := json.Marshal(map[string]string{"path": "sample.txt", "old_text": "before", "new_text": strings.Repeat("x", size)})
	var stream strings.Builder
	for offset := 0; offset < len(args); offset += 128 {
		delta := map[string]any{"index": 0, "function": map[string]any{"arguments": string(args[offset:min(len(args), offset+128)])}}
		if offset == 0 {
			delta["id"] = "edit-1"
			delta["type"] = "function"
			delta["function"].(map[string]any)["name"] = "propose_edit"
		}
		frame, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{delta}}}}})
		fmt.Fprintf(&stream, "data: %s\n\n", frame)
	}
	stream.WriteString("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
	return stream.String(), string(args)
}
func TestFragmentedLargeToolArgumentsPreserveBytes(t *testing.T) {
	stream, want := fragmentedToolStream(64 << 10)
	message, err := readStream(strings.NewReader(stream), func(string) error { t.Fatal("tool arguments emitted as text"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(message.ToolCalls) != 1 || message.ToolCalls[0].ID != "edit-1" || message.ToolCalls[0].Function.Name != "propose_edit" || message.ToolCalls[0].Function.Arguments != want {
		t.Fatal("fragmented tool arguments corrupted")
	}
}
func BenchmarkAgentFragmentedToolArguments(b *testing.B) {
	stream, _ := fragmentedToolStream(64 << 10)
	b.ReportAllocs()
	b.SetBytes(int64(len(stream)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := readStream(strings.NewReader(stream), func(string) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func TestStreamCallBufferRejectsGrowthBeforeMutation(t *testing.T) {
	for _, field := range []string{"id", "type", "name", "arguments"} {
		t.Run(field, func(t *testing.T) {
			var b streamCallBuffer
			var initial, extra streamCall
			switch field {
			case "id":
				initial.ID = strings.Repeat("i", 256)
				extra.ID = "x"
			case "type":
				initial.Type = strings.Repeat("t", 32)
				extra.Type = "x"
			case "name":
				initial.Function.Name = strings.Repeat("n", 64)
				extra.Function.Name = "x"
			case "arguments":
				initial.Function.Arguments = strings.Repeat("a", MaxFileBytes*2)
				extra.Function.Arguments = "x"
			}
			if err := b.append(initial); err != nil {
				t.Fatal(err)
			}
			before := b.message()
			if err := b.append(extra); err == nil {
				t.Fatal("stream field limit bypassed")
			}
			if after := b.message(); after != before {
				t.Fatal("rejected fragment mutated the accumulator")
			}
		})
	}
}
