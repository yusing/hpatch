[
	.[]
	| select(.type == "item.completed" and .item.type == "agent_message")
	| .item.text
	| select(test("^Tokens: i=[0-9]+, ci=[0-9]+, o=[0-9]+, r=[0-9]+$") | not)
][-1] == $expected
