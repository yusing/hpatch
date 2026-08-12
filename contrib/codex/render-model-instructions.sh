#!/bin/sh
set -eu

append_if_missing=false
if [ "$#" -eq 2 ] && [ "$1" = "--append-if-missing" ]; then
	append_if_missing=true
	shift
fi
if [ "$#" -ne 1 ]; then
	printf 'usage: %s [--append-if-missing] MODEL_INSTRUCTIONS\n' "$0" >&2
	exit 2
fi

input=$1
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
source_file=$script_dir/file-editing-instructions.md

start_marker='<!-- hpatch-model-instructions:start -->'
end_marker='<!-- hpatch-model-instructions:end -->'
stock_heading='## File editing constraints'
stock_instruction='Use `apply_patch` for local file edits. Do not create or edit files with `cat` or other shell write tricks. Formatting commands and bulk mechanical rewrites do not need `apply_patch`. Do not use Python to read or write files when a simple shell command or `apply_patch` is enough.'
stock_rg_instruction='- When you search for text or files, you reach first for `rg` or `rg --files`; they are much faster than alternatives like `grep`. If `rg` is unavailable, you use the next best tool without fuss.'
stock_exec_instruction='- Exercise caution when escaping text for exec_command calls - backticks and `$()` passed to the `cmd` argument will still execute. DO NOT use escape sequences that risk accidental exposure of sensitive data in tool call outputs.'
legacy_heading='## File editing'
legacy_instruction='Use `functions.hpatch` for local file edits, not `apply_patch`.'
legacy_read_heading='## File reading, searching, and shell commands'

count_exact() {
	grep -Fxc -- "$1" "$2" || true
}

line_exact() {
	line=$(grep -nFx -- "$1" "$2")
	printf '%s\n' "${line%%:*}"
}

render_range() {
	start=$1
	end=$2
	head -n "$((start - 1))" "$input"
	cat "$source_file"
	tail -n "+$((end + 1))" "$input"
}

render_stock() {
	start=$1
	end=$2
	rg_line=$(line_exact "$stock_rg_instruction" "$input")
	exec_line=$(line_exact "$stock_exec_instruction" "$input")
	awk -v start="$start" -v end="$end" -v rg_line="$rg_line" -v exec_line="$exec_line" \
		-v source_file="$source_file" '
		NR == start {
			while ((getline source_line < source_file) > 0) {
				print source_line
			}
			close(source_file)
		}
		(NR >= start && NR <= end) || NR == rg_line || NR == exec_line {
			next
		}
		{ print }
	' "$input"
}

if [ "$(count_exact "$start_marker" "$source_file")" -ne 1 ] ||
	[ "$(count_exact "$end_marker" "$source_file")" -ne 1 ]; then
	printf 'central Codex instructions must contain one start and end marker\n' >&2
	exit 1
fi

marked_starts=$(count_exact "$start_marker" "$input")
marked_ends=$(count_exact "$end_marker" "$input")
if [ "$marked_starts" -ne 0 ] || [ "$marked_ends" -ne 0 ]; then
	if [ "$marked_starts" -ne 1 ] || [ "$marked_ends" -ne 1 ]; then
		printf 'configured model instructions contain incomplete hpatch markers\n' >&2
		exit 1
	fi
	start=$(line_exact "$start_marker" "$input")
	end=$(line_exact "$end_marker" "$input")
	if [ "$start" -ge "$end" ]; then
		printf 'configured model instructions contain reversed hpatch markers\n' >&2
		exit 1
	fi
	render_range "$start" "$end"
	exit 0
fi

if [ "$(count_exact "$legacy_heading" "$input")" -eq 1 ] &&
	[ "$(count_exact "$legacy_instruction" "$input")" -eq 1 ] &&
	[ "$(count_exact "$legacy_read_heading" "$input")" -eq 1 ]; then
	start=$(line_exact "$legacy_heading" "$input")
	instruction=$(line_exact "$legacy_instruction" "$input")
	read_heading=$(line_exact "$legacy_read_heading" "$input")
	if [ "$instruction" -ne "$((start + 2))" ] || [ "$read_heading" -le "$instruction" ]; then
		printf 'legacy hpatch instructions do not have the expected section shape\n' >&2
		exit 1
	fi
	end=$(awk -v read_heading="$read_heading" '
		NR > read_heading && /^#{1,2} / {
			print NR - 1
			found = 1
			exit
		}
		END {
			if (!found) {
				print NR
			}
		}
	' "$input")
	render_range "$start" "$end"
	exit 0
fi

if [ "$(count_exact "$stock_heading" "$input")" -ne 1 ] ||
	[ "$(count_exact "$stock_instruction" "$input")" -ne 1 ] ||
	[ "$(count_exact "$stock_rg_instruction" "$input")" -ne 1 ] ||
	[ "$(count_exact "$stock_exec_instruction" "$input")" -ne 1 ]; then
	if [ "$append_if_missing" = true ] &&
		[ "$(count_exact "$legacy_heading" "$input")" -eq 0 ] &&
		[ "$(count_exact "$legacy_instruction" "$input")" -eq 0 ] &&
		[ "$(count_exact "$legacy_read_heading" "$input")" -eq 0 ] &&
		[ "$(count_exact "$stock_heading" "$input")" -eq 0 ] &&
		[ "$(count_exact "$stock_instruction" "$input")" -eq 0 ] &&
		[ "$(count_exact "$stock_rg_instruction" "$input")" -eq 0 ] &&
		[ "$(count_exact "$stock_exec_instruction" "$input")" -eq 0 ]; then
		cat "$input"
		if [ -s "$input" ]; then
			if [ -n "$(tail -c 1 "$input")" ]; then
				printf '\n'
			fi
			printf '\n'
		fi
		cat "$source_file"
		exit 0
	fi
	printf 'model instructions match neither stock, legacy hpatch, nor marked hpatch guidance\n' >&2
	exit 1
fi

start=$(line_exact "$stock_heading" "$input")
end=$(line_exact "$stock_instruction" "$input")
if [ "$end" -ne "$((start + 2))" ]; then
	printf 'stock file-editing heading and instruction are not one section\n' >&2
	exit 1
fi
render_stock "$start" "$end"
