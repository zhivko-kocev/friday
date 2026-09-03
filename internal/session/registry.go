package session

// Readers returns one fresh Reader per supported agent, in a fixed,
// deterministic order. Every reader is a stateless value (no setup cost),
// so building a new slice on each call is cheap and avoids any shared
// mutable state between callers — mirrors internal/presets.AllAdapters
// returning a fresh map rather than a shared package-level one.
func Readers() []Reader {
	return []Reader{
		NewClaudeReader(),
		NewCodexReader(),
		NewCopilotReader(),
		NewGeminiReader(),
		NewOpencodeReader(),
		NewOhMyPiReader(),
	}
}

// Names returns every supported agent name, in the same order as Readers.
func Names() []string {
	readers := Readers()
	out := make([]string, len(readers))
	for i, r := range readers {
		out[i] = r.Agent()
	}
	return out
}
