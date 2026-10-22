package toolpresentation

// The shipped catalog: the built-in tools of every runtime withHuman
// intercepts, and the MCP tools whose names are distinctive enough to
// recognise without knowing the server. A field named here that a
// particular call does not carry is skipped, and anything the call
// carries that is not named here still shows, so a guess at a payload
// shape can only ever fail to help, never mislead.

func text(path, label string) Field     { return Field{Path: path, Label: label, Kind: KindText} }
func prose(path, label string) Field    { return Field{Path: path, Label: label, Kind: KindProse} }
func md(path, label string) Field       { return Field{Path: path, Label: label, Kind: KindMarkdown} }
func html(path, label string) Field     { return Field{Path: path, Label: label, Kind: KindHTML} }
func code(path, label string) Field     { return Field{Path: path, Label: label, Kind: KindCode} }
func shell(path, label string) Field    { return Field{Path: path, Label: label, Kind: KindShell} }
func diff(path, label string) Field     { return Field{Path: path, Label: label, Kind: KindDiff} }
func filePath(path, label string) Field { return Field{Path: path, Label: label, Kind: KindPath} }
func link(path, label string) Field     { return Field{Path: path, Label: label, Kind: KindURL} }
func email(path, label string) Field    { return Field{Path: path, Label: label, Kind: KindEmail} }
func ref(path, label string) Field      { return Field{Path: path, Label: label, Kind: KindReference} }
func enum(path, label string) Field     { return Field{Path: path, Label: label, Kind: KindEnum} }
func when(path, label string) Field     { return Field{Path: path, Label: label, Kind: KindDateTime} }
func list(path, label string, item *Field) Field {
	return Field{Path: path, Label: label, Kind: KindList, Item: item}
}
func change(before, after, label string) Field {
	return Field{Label: label, Kind: KindDiff, Before: before, After: after}
}
func detail(field Field) Field { field.Role = RoleDetail; return field }
func emails(path, label string) Field {
	return list(path, label, &Field{Kind: KindEmail})
}

func runtimeEntry(runtime, tool, title string, fields ...Field) Entry {
	return Entry{Runtime: runtime, Tool: tool, Spec: Spec{Title: title, Fields: fields}}
}

func serverEntry(server, tool, title string, fields ...Field) Entry {
	return Entry{Server: server, Tool: tool, Spec: Spec{Title: title, Fields: fields}}
}

func toolEntry(tool, title string, fields ...Field) Entry {
	return Entry{Tool: tool, Spec: Spec{Title: title, Fields: fields}}
}

// Argument shapes shared by several runtimes.
var (
	runCommand    = []Field{shell("/command", "Command"), prose("/description", "Description"), detail(text("/timeout", "Timeout")), detail(text("/run_in_background", "In background"))}
	writeFile     = []Field{filePath("/file_path", "File"), code("/content", "Content")}
	writePath     = []Field{filePath("/path", "File"), code("/content", "Content")}
	editFile      = []Field{filePath("/file_path", "File"), change("/old_string", "/new_string", "Change"), detail(text("/replace_all", "Replace all"))}
	editPathText  = []Field{filePath("/path", "File"), change("/oldText", "/newText", "Change")}
	editPathSnake = []Field{filePath("/path", "File"), change("/old_string", "/new_string", "Change")}
)

var builtin = []Entry{
	// Claude Code.
	runtimeEntry("claude-code", "Bash", "Run a shell command", runCommand...),
	runtimeEntry("claude-code", "Edit", "Edit {{/file_path}}", editFile...),
	runtimeEntry("claude-code", "Write", "Write {{/file_path}}", writeFile...),
	runtimeEntry("claude-code", "MultiEdit", "Edit {{/file_path}}",
		filePath("/file_path", "File"),
		list("/edits", "Edits", &Field{Kind: KindDiff, Before: "old_string", After: "new_string"})),
	runtimeEntry("claude-code", "NotebookEdit", "Edit notebook {{/notebook_path}}",
		filePath("/notebook_path", "Notebook"), enum("/edit_mode", "Mode"), enum("/cell_type", "Cell type"),
		code("/new_source", "New source"), detail(ref("/cell_id", "Cell"))),
	runtimeEntry("claude-code", "Read", "Read {{/file_path}}", filePath("/file_path", "File"), detail(text("/offset", "Offset")), detail(text("/limit", "Limit"))),
	runtimeEntry("claude-code", "Glob", "Find files", code("/pattern", "Pattern"), filePath("/path", "In")),
	runtimeEntry("claude-code", "Grep", "Search files", code("/pattern", "Pattern"), filePath("/path", "In"), text("/glob", "Files"), detail(enum("/output_mode", "Output"))),
	runtimeEntry("claude-code", "WebFetch", "Fetch a web page", link("/url", "URL"), prose("/prompt", "Prompt")),
	runtimeEntry("claude-code", "WebSearch", "Search the web", text("/query", "Query")),
	runtimeEntry("claude-code", "Task", "Delegate to a subagent", text("/description", "Task"), enum("/subagent_type", "Agent"), prose("/prompt", "Prompt")),

	// Codex: its hook spells the shell tool Bash, and edits arrive as one
	// apply_patch document.
	runtimeEntry("codex", "Bash", "Run a shell command", runCommand...),
	runtimeEntry("codex", "apply_patch", "Apply a patch", diff("/input", "Patch"), diff("/patch", "Patch")),

	// OpenClaw.
	runtimeEntry("openclaw", "exec", "Run a shell command", runCommand...),
	runtimeEntry("openclaw", "write", "Write {{/path}}", writePath...),
	runtimeEntry("openclaw", "edit", "Edit {{/path}}", editPathText...),
	runtimeEntry("openclaw", "apply_patch", "Apply a patch", diff("/patch", "Patch"), diff("/input", "Patch")),
	runtimeEntry("openclaw", "read", "Read {{/path}}", filePath("/path", "File")),
	runtimeEntry("openclaw", "message", "Send a message", text("/to", "To"), text("/channel", "Channel"), prose("/message", "Message"), prose("/text", "Message")),

	// Pi.
	runtimeEntry("pi", "bash", "Run a shell command", runCommand...),
	runtimeEntry("pi", "write", "Write {{/path}}", writePath...),
	runtimeEntry("pi", "edit", "Edit {{/path}}", editPathText...),
	runtimeEntry("pi", "read", "Read {{/path}}", filePath("/path", "File")),

	// Hermes Agent.
	runtimeEntry("hermes", "terminal", "Run a shell command", runCommand...),
	runtimeEntry("hermes", "process", "Manage a process", enum("/action", "Action"), text("/pid", "Process"), text("/session_id", "Session"), shell("/command", "Command")),
	runtimeEntry("hermes", "execute_code", "Run code", code("/code", "Code"), detail(enum("/language", "Language"))),
	runtimeEntry("hermes", "patch", "Patch {{/path}}", editPathSnake...),
	runtimeEntry("hermes", "write_file", "Write {{/path}}", writePath...),
	runtimeEntry("hermes", "read_file", "Read {{/path}}", filePath("/path", "File")),

	// DeepSeek Harness.
	runtimeEntry("deepseek", "bash", "Run a shell command", runCommand...),
	runtimeEntry("deepseek", "run_code", "Run code", code("/code", "Code"), detail(enum("/language", "Language"))),
	runtimeEntry("deepseek", "write", "Write {{/path}}", writePath...),
	runtimeEntry("deepseek", "edit", "Edit {{/path}}", editPathSnake...),
	runtimeEntry("deepseek", "read", "Read {{/path}}", filePath("/path", "File")),

	// Superhuman mail. The tool names are distinctive enough to bind
	// without the server alias, which each person picks.
	toolEntry("create_or_update_draft", "Email draft",
		enum("/type", "Type"), email("/from", "From"), emails("/to", "To"), emails("/cc", "Cc"), emails("/bcc", "Bcc"),
		text("/subject", "Subject"), html("/body", "Body"),
		detail(email("/acting_email", "Account")), detail(ref("/thread_id", "Thread")), detail(ref("/draft_id", "Draft"))),
	toolEntry("send_draft", "Send an email draft", ref("/draft_id", "Draft"), detail(email("/acting_email", "From"))),
	toolEntry("create_or_update_event", "Calendar event",
		text("/title", "Title"), when("/start", "Starts"), when("/end", "Ends"), emails("/attendees", "Attendees"),
		text("/location", "Location"), prose("/description", "Description"),
		detail(email("/acting_email", "Calendar")), detail(ref("/event_id", "Event"))),
	toolEntry("create_or_update_reminder", "Reminder", when("/remind_at", "When"), prose("/note", "Note"),
		detail(ref("/thread_id", "Thread")), detail(email("/acting_email", "Account"))),
	toolEntry("create_or_update_snippet", "Snippet", text("/name", "Name"), html("/body", "Body"), detail(email("/acting_email", "Account"))),
	toolEntry("update_thread", "Update a thread", ref("/thread_id", "Thread"), detail(email("/acting_email", "Account"))),
	toolEntry("trash_thread", "Trash a thread", ref("/thread_id", "Thread"), detail(email("/acting_email", "Account"))),
	toolEntry("mark_spam", "Mark as spam", ref("/thread_id", "Thread"), detail(email("/acting_email", "Account"))),
	toolEntry("unsubscribe", "Unsubscribe", ref("/thread_id", "Thread"), detail(email("/acting_email", "Account"))),

	// Generic mail tools (Gmail-style MCP servers).
	toolEntry("send_email", "Send an email", emails("/to", "To"), emails("/cc", "Cc"), emails("/bcc", "Bcc"),
		text("/subject", "Subject"), prose("/body", "Body"), html("/html_body", "Body")),
	toolEntry("draft_email", "Email draft", emails("/to", "To"), emails("/cc", "Cc"), emails("/bcc", "Bcc"),
		text("/subject", "Subject"), prose("/body", "Body"), html("/html_body", "Body")),

	// GitHub's MCP server.
	serverEntry("github", "create_issue", "New issue in {{/owner}}/{{/repo}}",
		text("/title", "Title"), md("/body", "Body"), list("/labels", "Labels", nil), list("/assignees", "Assignees", nil),
		detail(text("/owner", "Owner")), detail(text("/repo", "Repository"))),
	serverEntry("github", "update_issue", "Update issue #{{/issue_number}} in {{/owner}}/{{/repo}}",
		text("/title", "Title"), md("/body", "Body"), enum("/state", "State"), list("/labels", "Labels", nil),
		detail(text("/owner", "Owner")), detail(text("/repo", "Repository"))),
	serverEntry("github", "add_issue_comment", "Comment on #{{/issue_number}} in {{/owner}}/{{/repo}}",
		md("/body", "Comment"), detail(text("/owner", "Owner")), detail(text("/repo", "Repository"))),
	serverEntry("github", "create_pull_request", "Pull request in {{/owner}}/{{/repo}}",
		text("/title", "Title"), md("/body", "Body"), text("/head", "From branch"), text("/base", "Into branch"),
		detail(text("/draft", "Draft")), detail(text("/owner", "Owner")), detail(text("/repo", "Repository"))),
	serverEntry("github", "merge_pull_request", "Merge #{{/pullNumber}} in {{/owner}}/{{/repo}}",
		enum("/merge_method", "Method"), text("/commit_title", "Commit title"), prose("/commit_message", "Commit message"),
		detail(text("/owner", "Owner")), detail(text("/repo", "Repository"))),
	serverEntry("github", "create_or_update_file", "Write {{/path}} in {{/owner}}/{{/repo}}",
		filePath("/path", "File"), code("/content", "Content"), prose("/message", "Commit message"), text("/branch", "Branch"),
		detail(text("/owner", "Owner")), detail(text("/repo", "Repository")), detail(ref("/sha", "Replaces"))),
	serverEntry("github", "push_files", "Push to {{/branch}} in {{/owner}}/{{/repo}}",
		prose("/message", "Commit message"), text("/branch", "Branch"),
		list("/files", "Files", &Field{Kind: KindCode, Path: "content", Label: "Content"}),
		detail(text("/owner", "Owner")), detail(text("/repo", "Repository"))),
	serverEntry("github", "delete_file", "Delete {{/path}} in {{/owner}}/{{/repo}}",
		filePath("/path", "File"), prose("/message", "Commit message"), text("/branch", "Branch"),
		detail(text("/owner", "Owner")), detail(text("/repo", "Repository"))),
	serverEntry("github", "create_branch", "Branch {{/branch}} in {{/owner}}/{{/repo}}",
		text("/branch", "Branch"), text("/from_branch", "From"), detail(text("/owner", "Owner")), detail(text("/repo", "Repository"))),

	// Slack's MCP server.
	toolEntry("slack_post_message", "Post to Slack", ref("/channel_id", "Channel"), md("/text", "Message")),
	toolEntry("slack_reply_to_thread", "Reply in a Slack thread", ref("/channel_id", "Channel"), md("/text", "Message"), detail(ref("/thread_ts", "Thread"))),

	// Linear's MCP server.
	serverEntry("linear", "create_issue", "New Linear issue", text("/title", "Title"), md("/description", "Description"),
		enum("/priority", "Priority"), detail(ref("/teamId", "Team")), detail(ref("/team", "Team")), detail(ref("/assigneeId", "Assignee"))),
	serverEntry("linear", "update_issue", "Update Linear issue", ref("/id", "Issue"), text("/title", "Title"),
		md("/description", "Description"), enum("/priority", "Priority"), detail(ref("/stateId", "State"))),
	serverEntry("linear", "create_comment", "Comment on a Linear issue", ref("/issueId", "Issue"), md("/body", "Comment")),

	// Stripe's MCP server.
	serverEntry("stripe", "create_refund", "Refund", ref("/payment_intent", "Payment"), text("/amount", "Amount"), enum("/reason", "Reason")),
	serverEntry("stripe", "create_customer", "New customer", text("/name", "Name"), email("/email", "Email")),
	serverEntry("stripe", "create_payment_link", "Payment link", ref("/price", "Price"), text("/quantity", "Quantity")),

	// The reference filesystem MCP server.
	serverEntry("filesystem", "write_file", "Write {{/path}}", writePath...),
	serverEntry("filesystem", "edit_file", "Edit {{/path}}", filePath("/path", "File"),
		list("/edits", "Edits", &Field{Kind: KindDiff, Before: "oldText", After: "newText"}), detail(text("/dryRun", "Dry run"))),
	serverEntry("filesystem", "move_file", "Move {{/source}}", filePath("/source", "From"), filePath("/destination", "To")),
}

// Builtin returns the shipped catalog, in order.
func Builtin() []Entry {
	out := make([]Entry, len(builtin))
	copy(out, builtin)
	return out
}
