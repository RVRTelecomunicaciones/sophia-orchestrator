# Delta: mcp-stdio-client-adapter

## Capability

A go-sdk `CommandTransport` wrapper that connects to a stdio MCP subprocess, performs the MCP initialize handshake, dispatches `CallTool` round-trips, and tears down the subprocess cleanly on close.

## ADDED Requirements

### Requirement: Subprocess Connect

The adapter MUST spawn the target subprocess using `mcp.CommandTransport` and complete the MCP initialize handshake via `client.Connect` before any tool call is dispatched. The resulting client session (`*mcp.ClientSession`) MUST be held for the lifetime of the connection.

#### Scenario: Successful connection

- GIVEN a valid executable path and arguments
- WHEN `Connect` is called with a non-expired context
- THEN the subprocess is spawned, the MCP initialize handshake completes without error
- AND the adapter returns a live `ClientSession` ready for tool calls

#### Scenario: Startup timeout exceeded

- GIVEN a `startup_timeout_s` value that expires before the subprocess responds
- WHEN `Connect` is called
- THEN the adapter returns a typed startup-timeout error
- AND no subprocess is left running

#### Scenario: Executable not found

- GIVEN a binary path that does not exist on `PATH`
- WHEN `Connect` is called
- THEN the adapter returns an error immediately
- AND no goroutine or file descriptor is leaked

### Requirement: CallTool Round-Trip

The adapter MUST forward a `CallTool` request to the connected subprocess and return the raw `*mcp.CallToolResult` to the caller. The adapter MUST NOT mutate or filter the result payload.

#### Scenario: Successful tool call

- GIVEN a live `ClientSession`
- WHEN `CallTool` is invoked with a valid tool name and arguments map
- THEN the subprocess receives the request and the adapter returns the result without modification

#### Scenario: Subprocess returns an error result

- GIVEN a live `ClientSession`
- WHEN the subprocess returns a result with `IsError: true`
- THEN the adapter propagates that result to the caller without converting it to a Go error

#### Scenario: Context cancelled mid-call

- GIVEN a live `ClientSession`
- WHEN the caller's context is cancelled while a `CallTool` is in-flight
- THEN the adapter returns a context error promptly
- AND the underlying subprocess connection is not permanently corrupted

### Requirement: Clean Close

The adapter MUST call the SDK's `ClientSession.Close` (which sends SIGTERM then SIGKILL per the MCP spec) when `Close` is invoked. The adapter MUST NOT leak goroutines or OS processes after `Close` returns.

#### Scenario: Normal close

- GIVEN a connected adapter
- WHEN `Close` is called
- THEN the subprocess receives SIGTERM and exits
- AND no goroutines started by the adapter remain running after `Close` returns

#### Scenario: Close after context cancel

- GIVEN a connected adapter whose parent context has been cancelled
- WHEN `Close` is called
- THEN the subprocess is still terminated (SIGTERM/SIGKILL)
- AND no goroutine or process leak occurs
