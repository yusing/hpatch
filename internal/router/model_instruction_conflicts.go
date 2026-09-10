package router

import "strings"

// Pinned conflicting fragments from the GPT-6 and GPT-5.6 model templates and
// the active Codex prompt. Keep unrelated policy and tool-independent safety
// guidance intact. Apply outside our marked section too, so inherited prompts
// do not retain conflicts from an earlier rewrite.
var stockToolConflictReplacer = strings.NewReplacer(
	"Put this explanation in a short, separate paragraph at the end of both commentary and final, after any permission question.",
	"Put this explanation in a short, separate paragraph at the end of the final answer, after any permission question. Attach a progress copy only when a supported tool commentary mechanism is available.",
	"Do NOT send user facing questions in intermediate commentary messages. Do NOT put a final response in the commentary channel. The final answer must always be fully self-contained: users should never need to read earlier commentary updates, since they are collapsed after the final answer is shown to users.",
	"Keep user-facing questions out of progress notices. Deliver final responses in the final channel. The final answer must always be fully self-contained: users should never need to read earlier progress updates.",
	"Do NOT put a final response (e.g. a blocking / clarifying question) in the commentary channel that should be asked in the final channel. Messages to users in the commentary channel are only for partial updates, partial results, or non-blocking questions that can provide value to users while the AI assistant continues working. The final answer must always be fully self-contained: users should never need to read earlier commentary updates, since they are collapsed after the final answer is shown to users.",
	"Deliver final responses and blocking questions in the final channel. Attach partial updates, partial results, or non-blocking questions to supported tool calls when available while continuing work. The final answer must always be fully self-contained: users should never need to read earlier progress updates.",
	"- You share updates in the `commentary` channel.",
	"- You share updates through the supported tool commentary mechanism described below.",
	"As you work, you use the `commentary` channel to share concise, meaningful updates including relevant assumptions, findings, decisions, or changes in direction.",
	"As you work, attach concise, meaningful updates to supported tool calls, including relevant assumptions, findings, decisions, or changes in direction.",
	"As you work, you send messages to the `commentary` channel.",
	"As you work, attach progress messages to supported tool calls.",
	"If the user's request requires calling tools, start with a message in the `commentary` channel. The user appreciates consistent, frequent communication during your turn, and should not be left without a commentary update for more than 60 seconds during ongoing work.",
	"Use the central Commentary rules for progress delivery; when no available tool supports commentary, continue silently.",
	"The first time in a conversation that you decide to apply a skill, inform the user in the commentary channel.",
	"The first time in a conversation that you decide to apply a skill, attach that notice to a supported tool call when available.",
	"Explicitly tell the user in the `commentary` channel whenever a skill causes you to take an action or pause your work.",
	"Attach skill-related progress notices to supported tool calls when available; report a blocking issue in the final answer if no such call is available.",
	"- First, tell the user in the commentary channel **why** you are using the skill.",
	"- Attach why you are using the skill to a supported tool call when available.",
	"answer briefly in commentary, then resume the active task",
	"answer briefly through supported tool commentary when available, then resume the active task",
	"- Batch independent searches and reads in one functions.exec using await Promise.allSettled([...]); inspect every result. Keep dependencies, edits, approvals, waits, and adaptive follow-ups sequential. Avoid unnecessary output.",
	"- Batch already-known searches and reads in one functions.shell script; inspect every result. Keep dependencies, edits, approvals, waits, and adaptive follow-ups sequential. Avoid unnecessary output.",
	"- Batch independent searches, reads, and other tool calls in one functions.exec using await Promise.allSettled([...]); keep each batch bounded to decision-relevant output by selecting needed ranges or fields first, and inspect every returned result. If output truncates, retrieve only the missing evidence rather than repeating an unchanged whole scan. Keep dependencies, edits, approvals, waits, and adaptive follow-ups sequential. Avoid unnecessary output.",
	"- Batch already-known searches and reads in one functions.shell script; bound output to needed ranges or fields and inspect every result. If output truncates, retrieve only missing evidence. Keep dependencies, edits, approvals, waits, and adaptive follow-ups sequential.",
	"- When calling `functions.exec`, parallelize independent tool calls by awaiting Promises. Dependent operations, approvals, mutations, or operations that may not parallelize cleanly, can be sequential.",
	"- Parallelize independent calls only when their tool contracts allow it. Run hpatch alone; sequence dependent operations, approvals, and mutations.",
	"- When possible, prefer parallelization over sequential tool calls, as this will help with round-trip latency and let you get work done faster.",
	"- Parallelize independent calls only when their tool contracts allow it. Run hpatch alone; sequence dependent operations, approvals, and mutations.",
	"- Avoid performing blocking sleep or wait calls longer than 60 seconds, as they may prevent you from communicating with the user for their duration.",
	"- Use completion notifications or interruptible waits; do not shorten waits solely to emit commentary.",
)

func rewriteStockToolConflicts(input string) string {
	return stockToolConflictReplacer.Replace(input)
}
