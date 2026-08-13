#!/bin/sh
set -eu

if [ "$#" -ne 0 ]; then
	printf 'usage: %s\n' "$0" >&2
	exit 2
fi

config_file=${CODEX_CONFIG_FILE:-${CODEX_HOME:-"$HOME/.codex"}/config.toml}
invocation_directory=$PWD
case $config_file in
	/*) ;;
	*) config_file=$invocation_directory/$config_file ;;
esac
config_directory=$(dirname -- "$config_file")
default_target=$config_directory/hpatch-model-instructions.md
agents_directory=$config_directory/agents

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
start_marker='<!-- hpatch-model-instructions:start -->'
end_marker='<!-- hpatch-model-instructions:end -->'

tmp=$(mktemp -d)
cleanup() {
	rm -rf "$tmp"
}
trap cleanup EXIT HUP INT TERM

strip_instructions() {
	target=$1
	[ -f "$target" ] || return

	starts=$(grep -Fxc -- "$start_marker" "$target" || true)
	ends=$(grep -Fxc -- "$end_marker" "$target" || true)
	if [ "$starts" -eq 0 ] && [ "$ends" -eq 0 ]; then
		return
	fi
	if [ "$starts" -ne 1 ] || [ "$ends" -ne 1 ]; then
		printf 'configured model instructions contain incomplete hpatch markers: %s\n' "$target" >&2
		exit 1
	fi
	start=$(grep -nFx -- "$start_marker" "$target")
	start=${start%%:*}
	end=$(grep -nFx -- "$end_marker" "$target")
	end=${end%%:*}
	if [ "$start" -ge "$end" ]; then
		printf 'configured model instructions contain reversed hpatch markers: %s\n' "$target" >&2
		exit 1
	fi

	awk -v start="$start_marker" -v end="$end_marker" '
		$0 == start { removing = 1; next }
		$0 == end { removing = 0; next }
		!removing { print }
	' "$target" >"$tmp/stripped"
	cat "$tmp/stripped" >"$target"
	printf 'removed hpatch model instructions from %s\n' "$target"
}

configured=false
target=
if [ -f "$config_file" ]; then
	go run "$script_dir/configvalue" "$config_file" >"$tmp/config.json"
	configured=$(jq -r '.found' "$tmp/config.json")
	if [ "$configured" = true ]; then
		target=$(jq -er '.path' "$tmp/config.json")
		case $target in
			/*) ;;
			*)
				printf 'model_instructions_file must be an absolute path: %s\n' "$target" >&2
				exit 1
				;;
		esac
	fi
fi

if [ "$configured" = true ] && [ "$target" = "$default_target" ]; then
	rm -f "$default_target"
	awk '
		/^[[:space:]]*\[/ { in_table = 1 }
		!in_table && /^[[:space:]]*("model_instructions_file"|model_instructions_file)[[:space:]]*=/ { next }
		{ print }
	' "$config_file" >"$tmp/config.toml"
	cat "$tmp/config.toml" >"$config_file"
	printf 'removed hpatch model instructions and config entry at %s\n' "$default_target"
elif [ "$configured" = true ]; then
	strip_instructions "$target"
fi

if [ -d "$agents_directory" ]; then
	for agent_config in "$agents_directory"/*.toml; do
		[ -f "$agent_config" ] || continue
		go run "$script_dir/configvalue" "$agent_config" >"$tmp/agent-config.json"
		if [ "$(jq -er '.found' "$tmp/agent-config.json")" != true ]; then
			continue
		fi
		agent_target=$(jq -er '.path' "$tmp/agent-config.json")
		case $agent_target in
			/*) ;;
			*) agent_target=$(dirname -- "$agent_config")/$agent_target ;;
		esac
		strip_instructions "$agent_target"
	done
fi
