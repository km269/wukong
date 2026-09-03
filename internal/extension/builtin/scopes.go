// scopes.go — per-tool permission scope declarations for built-in
// extensions (roadmap P0-1 Phase B).
//
// These declarations feed the capability registry descriptors so the
// Guard seam (internal/agent/loop.go commandToolNeedsValidation)
// can decide from declared metadata instead of tool-name
// heuristics. Declaring scopes for a tool opts it OUT of the legacy
// name heuristic: a declared tool validates commands only when the
// "shell" scope is present.
package builtin

// toolScopes maps extension name → tool name → permission scopes.
// Scope vocabulary: "shell" (command execution), "fs.write"
// (mutating filesystem access), "network" (outbound requests).
var toolScopes = map[string]map[string][]string{
	"developer": {
		"developer_command_execute": {"shell"},
		"developer_file_write":      {"fs.write"},
		"developer_file_replace":    {"fs.write"},
	},
}

// ToolScopes returns the per-tool scope declarations of a built-in
// extension, or nil when the extension has none declared. The
// returned map must not be mutated.
func ToolScopes(extName string) map[string][]string {
	return toolScopes[extName]
}
