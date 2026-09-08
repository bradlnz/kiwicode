package main

import (
	"strings"
	"testing"
)

func TestCSharpGenericTypeColors(t *testing.T) {
	for _, source := range []string{
		"ICommandHandler<TRequest, TResult> handler;",
		"public class Handler : ICommandHandler<TRequest, TResult> {}",
		"void Handle(ICommandHandler<TRequest, TResult> handler, int count) {}",
		"ICommandHandler<List<TRequest>, Task<TResult[]>> handler;",
		"ICommandHandler<Demo.TRequest, Demo.TResult> handler;",
		"ICommandHandler<TRequest, TResult", // Incomplete while typing.
	} {
		colored := highlightLine("Handler.cs", []rune(source))
		for _, name := range []string{"TRequest", "TResult"} {
			if !strings.Contains(colored, paint(colors.typeName, name)) {
				t.Fatalf("%s should have type color in %q: %q", name, source, colored)
			}
		}
	}
	colored := highlightLine("Handler.cs", []rune("ICommandHandler<T, T> handler;"))
	if strings.Count(colored, paint(colors.typeName, "T")) != 2 {
		t.Fatal("both generic arguments must have type color")
	}
	for _, source := range []string{"item.Name = Value;", "var text = \"IHandler<First, Second>\";", "// IHandler<First, Second>"} {
		colored := highlightLine("Handler.cs", []rune(source))
		for _, name := range []string{"Name", "Value", "First", "Second"} {
			if strings.Contains(colored, paint(colors.typeName, name)) {
				t.Fatalf("colored a member, string or comment as a type: %q", source)
			}
		}
	}
}
