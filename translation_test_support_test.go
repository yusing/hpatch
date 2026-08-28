package hpatch

import "context"

func translateForTest(ctx context.Context, workspace Workspace, script string) ([]byte, error) {
	changes, _, report, aliases, err := evaluateScript(ctx, workspace, script)
	if err != nil {
		return nil, err
	}
	result := hostTranslationResult(changes, report, aliases, true)
	if err := translateHostResult(ctx, changes, &result); err != nil {
		return nil, err
	}
	return result.Patch, nil
}

func translateForHostForTest(ctx context.Context, workspace Workspace, script, dataDirectory string) (HostTranslation, error) {
	changes, _, report, aliases, err := evaluateScript(ctx, workspace, script)
	result := hostTranslationResult(changes, report, aliases, err == nil)
	failureStage := ""
	if err != nil {
		failureStage = "evaluated"
	} else if err = translateHostResult(ctx, changes, &result); err != nil {
		failureStage = "translated"
	}
	return finishHostChange(ctx, dataDirectory, script, result, failureStage, err, false)
}
