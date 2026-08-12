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
write_target=$target
if [ "$configure" = false ] && [ -L "$target" ]; then
	write_target=$(readlink -f -- "$target")
	if [ -z "$write_target" ] || [ ! -f "$write_target" ]; then
		printf 'configured model_instructions_file symlink has no regular-file referent: %s\n' "$target" >&2
		exit 1
	fi
fi
target_directory=$(dirname -- "$write_target")
install -d "$target_directory"
if [ "$configure" = false ]; then
	target_tmp=$(mktemp "$target_directory/.hpatch-instructions-XXXXXX")
	cp -p "$write_target" "$target_tmp"
	cat "$tmp/hpatch.md" >"$target_tmp"
	mv "$target_tmp" "$write_target"
	target_tmp=
else
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

printf 'installed Codex model instructions from %s at %s\n' "$selected" "$target"
