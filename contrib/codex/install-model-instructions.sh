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
default_target=$(dirname -- "$config_file")/hpatch-model-instructions.md

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
renderer=$script_dir/render-model-instructions.sh
model=${CODEX_MODEL:-}
agents_directory=$(dirname -- "$config_file")/agents
agent_count=0

tmp=$(mktemp -d)
config_tmp=
target_tmp=
cleanup() {
	rm -rf "$tmp"
	if [ -n "$config_tmp" ]; then
		rm -f "$config_tmp"
	fi
	if [ -n "$target_tmp" ]; then
		rm -f "$target_tmp"
	fi
}
trap cleanup EXIT HUP INT TERM

install_customized() {
	rendered=$1
	configured_target=$2
	write_target=$configured_target
	if [ -L "$configured_target" ]; then
		write_target=$(readlink -f -- "$configured_target")
		if [ -z "$write_target" ] || [ ! -f "$write_target" ]; then
			printf 'configured model_instructions_file symlink has no regular-file referent: %s\n' "$configured_target" >&2
			exit 1
		fi
	fi
	target_directory=$(dirname -- "$write_target")
	install -d "$target_directory"
	target_tmp=$(mktemp "$target_directory/.hpatch-instructions-XXXXXX")
	cp -p "$write_target" "$target_tmp"
	cat "$rendered" >"$target_tmp"
	mv "$target_tmp" "$write_target"
	target_tmp=
}

configured=false
if [ -f "$config_file" ]; then
	go run "$script_dir/configvalue" "$config_file" >"$tmp/config.json"
	configured=$(jq -r '.found' "$tmp/config.json")
fi

if [ "$configured" = true ]; then
	target=$(jq -er '.path' "$tmp/config.json")
	case $target in
		/*) ;;
		*)
			printf 'model_instructions_file must be an absolute path: %s\n' "$target" >&2
			exit 1
			;;
	esac
	if [ ! -f "$target" ]; then
		printf 'configured model_instructions_file does not exist: %s\n' "$target" >&2
		exit 1
	fi
	input=$target
	configure=false
	selected='configured file'
else
	target=$default_target
	codex debug models --bundled >"$tmp/models.json"
	if [ -z "$model" ]; then
		model=$(jq -er '.models | min_by(.priority) | .slug' "$tmp/models.json")
	fi
	jq -er --arg model "$model" \
		'.models[] | select(.slug == $model) | .base_instructions' \
		"$tmp/models.json" >"$tmp/base.md"
	input=$tmp/base.md
	configure=true
	selected=$model
fi

sh "$renderer" "$input" >"$tmp/hpatch.md"

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
		if [ ! -f "$agent_target" ]; then
			printf 'agent model_instructions_file does not exist: %s\n' "$agent_target" >&2
			exit 1
		fi
		agent_count=$((agent_count + 1))
		printf '%s' "$agent_target" >"$tmp/agent-$agent_count.path"
				sh "$renderer" --append-if-missing "$agent_target" >"$tmp/agent-$agent_count.md"

	done
fi

if [ "$configure" = false ]; then
	install_customized "$tmp/hpatch.md" "$target"
else
	target_directory=$(dirname -- "$target")
	install -d "$target_directory"
	install -m 0644 "$tmp/hpatch.md" "$target"
fi

if [ "$configure" = true ]; then
	config_directory=$(dirname -- "$config_file")
	install -d "$config_directory"
	encoded=$(jq -Rn --arg path "$target" '$path')
	config_tmp=$(mktemp "$config_directory/.hpatch-config-XXXXXX")
	if [ -f "$config_file" ]; then
		cp -p "$config_file" "$config_tmp"
	fi
	first_table=
	if [ -s "$config_file" ]; then
		first_table=$(grep -nE '^[[:space:]]*\[' "$config_file" | head -n 1 || true)
		first_table=${first_table%%:*}
	fi
	if [ -n "$first_table" ]; then
		{
			head -n "$((first_table - 1))" "$config_file"
			printf 'model_instructions_file = %s\n\n' "$encoded"
			tail -n "+$first_table" "$config_file"
		} >"$config_tmp"
	else
		if [ -s "$config_tmp" ] && [ -n "$(tail -c 1 "$config_tmp")" ]; then
			printf '\n' >>"$config_tmp"
		fi
		printf 'model_instructions_file = %s\n' "$encoded" >>"$config_tmp"
	fi
	mv "$config_tmp" "$config_file"
	config_tmp=
fi

agent_index=1
while [ "$agent_index" -le "$agent_count" ]; do
	agent_target=$(cat "$tmp/agent-$agent_index.path")
	install_customized "$tmp/agent-$agent_index.md" "$agent_target"
	printf 'installed Codex agent model instructions at %s\n' "$agent_target"
	agent_index=$((agent_index + 1))
done

printf 'installed Codex model instructions from %s at %s\n' "$selected" "$target"
