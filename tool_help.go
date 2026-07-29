package hpatch

const toolDescription = `HPATCH/1
call=functions.hpatch(raw_complete_script);forbid=shell|functions.exec;atomic(reject|cancel)=patch:none,workspace:unchanged
lex.command=nonblank_physical_line;newline=command_end;exception=type_heredoc;recovery=open_quote_across_newline=>one_header_owned_rejected_frame
lex.quote=JSON_double_quote;literal_tab=allow;physical_newline=never;linebreak_escape=\n|\r;escape=quote|backslash|LF|CR|NUL|C0
cmd=in PATH|new PATH|mv DESTINATION|rm|sel LINE START:END|tsel FROM_LINE "TEXT" [N]|bsel "START" "END"|rsel START:END|type "TEXT"|type <<TAG|del|copy|cut|paste|commit
path=workspace_relative_within_root;in=existing_regular_UTF-8;destination(new|mv)=free;parent(new|mv)=existing_directory
state.active=in|new=>select;mv DESTINATION=>rename_active_file(source_implicit);rm=>delete_active_file(no_operand)
state.coords=file.baseline[generation];earlier_edits_shift_coords=false;inserted_text_selectable=false
state.in=first_existing=>baseline:=file_bytes;known=>cursor:=BOF,selections:=none,edits:=keep
state.mv=path:=DESTINATION,baseline_id:=keep
state.commit=all_live_files{baseline:=rendered,edits:=none,cursor:=BOF,selections:=none,active:=keep};clipboard:=keep;partial_filesystem_write=false;whole_script_atomic=true
state.script_end=finalize_pending_without_reset
clipboard=script_scope,cross_file,repeat_paste,commit_preserves
sel=line[LINE].rune[START..END];index=1;inclusive;tab_runes=1
tsel=baseline[(FROM_LINE,col1)..EOF];TEXT=nonempty;match=exact+nonoverlap;select=first_N;N_default=1;require_N
tsel.TEXT=copy_exact_baseline_substring;start_at_first_nonwhitespace;exclude_leading_indent;encode_JSON_only;never_infer|normalize|paraphrase
tsel.repair=FROM_LINE_only;TEXT_unchanged;suffix_count<N&&file_count==N=>FROM_LINE:=line(first_match);file_count>N=>reject
bsel=START!=END;nonempty;START_count(file)==1&&END_count(suffix_after_START)==1;selection=START.first_byte..END.last_byte;outside_bytes_preserved;nearest_END=false
bsel.anchor_fallback=no_exact_match=>ASCII_space_tab_runs_equivalent
rsel=logical_line[START..END];index=1;inclusive;owns_terminators
selector.priority=tsel|rsel>bsel>sel;anchor=stable_nonwhitespace_content;never_include_leading_indent;whole_line|indent_edit=>rsel;tsel_TEXT=copy_longer_exact_baseline_span_before_occurrence_arithmetic
numeric_selector.coords=fresh_nl_-ba;within_script_rebase=forbid
heredoc.tag=[A-Za-z0-9_.-]{1,64};header=type_<<TAG|type_<<'TAG'|type_<<"TAG"
heredoc.close=unquoted_TAG_exact;indent=0;suffix=none
heredoc.body=literal_UTF-8;max_bytes=1048576
inline_linebreaks=type|bsel:encode_as_\n|\r;tsel:forbid_LF_CR
edit.type=selections?replace_each:insert_cursor;selections:=none
edit.multiselect=type|del|cut|paste;atomic
edit.del=delete_each_selection;requires=selection
edit.copy=clipboard:=first_selection_baseline_text;requires=selection;selections:=keep
edit.cut=clipboard:=first_selection_baseline_text+delete_each_selection;requires=selection;selections:=none
edit.paste=after_each_selection|cursor;requires=clipboard;selections:=none;clipboard:=keep
edit.linewise=type_without_final_terminator=>preserve_selected_LF|CRLF|CR;del|cut=>remove_complete_lines;paste=>insert_after_selection+add_missing_boundaries;nonlinewise_destination_may_split_line
edit.conflict=same_generation{overlap_replace_delete|insert_inside_replacement|same_offset_multi_insert};allow=disjoint|boundary_insert
file.new=active:=empty_pending;effective_type_count_per_generation<=1
file.rm=active:=none;existing_with_content_edits=>conflict
bsel.replace=both_anchors_consumed;following_terminator_outside_selection_unless_encoded_in_END;replacement_final_terminator_may_add_blank_line
result.success=active_path+cursor_or_selection_ranges<=3+last_effective_edit(path,operation,count,rendered_ranges<=3)+conditional_net_file_actions+postedit_context_lines<=3
result.selection_ranges=individual;locations>3=>first_3+omitted_count
result.file_actions=show_if lifecycle|multi_file|changed_file!=active;categories=updated|moved|moved+updated|added|deleted
result.preview=rendered_postedit;multi_location=>distinct_affected_lines<=3;single_location=>nearby_lines<=3;source_codepoints_per_line<=64;truncation_marker=none
result.noop=net_workspace_unchanged=>reject
result.tsel_repair=requested_line+resolved_line+postedit_context_lines<=3
result.failure=command_context+repair_context_if_available;repair_context_not_match_candidate;retry_baseline=unchanged
verify=inspect_reported_lines+formatter_or_parser+tests+whitespace_lint+git_diff_--check
`

// ToolDescription returns the authoritative free-form tool instructions.
func ToolDescription() string {
	return toolDescription
}
