package tools

// ToolScope describes the read / write / network / persistence footprint of a
// single tool invocation. All slices are treated as immutable after return —
// callers MUST NOT mutate them.
//
// Semantics:
//   - ReadPaths: absolute paths this call may read. Empty slice means "no
//     file reads". An entry equal to the guard's workDir means "any file under
//     workDir".
//   - WritePaths: absolute paths this call may write. This is a
//     scheduling / lock / concurrency hint consumed by the LBB3
//     partitioner for file-level locks and by PathGuard.CheckScope
//     for write-conflict detection — NOT a security boundary.
//     Actual filesystem write enforcement happens in PathGuard,
//     BashTool preflight validation, and (eventually) a sandbox
//     substrate; never rely on WritePaths alone to confine writes.
//   - Network: true if the call touches the network (HTTP, DNS, etc).
//   - Persistent: true if the call mutates external state that survives the
//     process (DB writes, file writes, git commits, remote RPC). Reads from
//     persistent stores are NOT persistent=true.
type ToolScope struct {
	ReadPaths  []string
	WritePaths []string
	Network    bool
	Persistent bool
}

// ConservativeScope is the fail-closed default used when params cannot be
// parsed. Treat as "I touch everything".
func ConservativeScope() ToolScope {
	return ToolScope{Network: true, Persistent: true}
}
