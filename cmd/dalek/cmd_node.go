package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"dalek/internal/app"
	nodeagentsvc "dalek/internal/services/nodeagent"
	snapshotsvc "dalek/internal/services/snapshot"
)

func cmdNode(args []string) {
	if len(args) == 0 {
		printNodeUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "add":
		cmdNodeAdd(args[1:])
	case "ls":
		cmdNodeList(args[1:])
	case "show":
		cmdNodeShow(args[1:])
	case "rm":
		cmdNodeRemove(args[1:])
	case "run-loop":
		cmdNodeRunLoop(args[1:])
	case "-h", "--help", "help":
		printNodeUsage()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 node 子命令: %s", sub),
			"node 命令组仅支持固定子命令",
			"运行 dalek node --help 查看可用命令",
		)
	}
}

func printNodeUsage() {
	printGroupUsage("Node Agent 运行命令", "dalek node <command> [flags]", []string{
		"add        添加节点",
		"ls         列出节点",
		"show       查看节点详情",
		"rm         删除节点",
		"run-loop   运行最小 node-agent loop（register/heartbeat/inspect）",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek node <command> --help\" for more information.")
}

func cmdNodeRunLoop(args []string) {
	fs := flag.NewFlagSet("node run-loop", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"运行最小 node-agent loop（支持 recovery / warnings 观测）",
			"dalek node run-loop --name <node> [--run-id <id> | --verify-target <target>] [--manifest-file <path> | --workspace-dir <path>] [--heartbeat-count 1] [--heartbeat-interval 0s] [--output text|json]",
			"dalek node run-loop --name node-c",
			"dalek node run-loop --name node-c --run-id 88 -o json",
			"dalek node run-loop --name node-c --verify-target test --snapshot-id snap-88 -o json",
			"dalek node run-loop --name node-c --verify-target test --snapshot-id snap-88 --manifest-file ./manifest.json -o json",
			"dalek node run-loop --name node-c --verify-target test --snapshot-id snap-88 --workspace-dir . -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	nodeName := fs.String("name", "", "node 名（必填）")
	endpoint := fs.String("endpoint", "", "node endpoint（可选）")
	runID := fs.Uint("run-id", 0, "inspect 的 run id（可选）")
	verifyTarget := fs.String("verify-target", "", "提交 run 使用的 verify target（可选）")
	requestID := fs.String("request-id", "", "run request id（可选）")
	snapshotID := fs.String("snapshot-id", "", "run 使用的 snapshot id（可选）")
	baseCommit := fs.String("base-commit", "", "run 使用的 base commit（可选）")
	workspaceGeneration := fs.String("workspace-generation", "", "snapshot 使用的 workspace generation（可选）")
	manifestFile := fs.String("manifest-file", "", "上传 snapshot 使用的 manifest.json 路径（可选）")
	workspaceDir := fs.String("workspace-dir", "", "从工作区生成并上传 snapshot manifest（可选）")
	waitForTerminal := fs.Bool("wait", false, "提交或查询 run 后等待 terminal 状态")
	pollInterval := fs.Duration("poll-interval", 1*time.Second, "等待 run terminal 的轮询间隔（默认 1s）")
	watchStage := fs.String("watch-stage", "", "当 run stage 等于此值时，继续监听直到变化")
	stageInterval := fs.Duration("stage-interval", 5*time.Second, "watch-stage 轮询间隔（默认 5s）")
	heartbeatCount := fs.Int("heartbeat-count", 1, "heartbeat 次数（默认 1）")
	heartbeatInterval := fs.Duration("heartbeat-interval", 0, "heartbeat 间隔（可选）")
	timeout := fs.Duration("timeout", 10*time.Second, "命令超时（例如 10s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "node run-loop 参数解析失败", "运行 dalek node run-loop --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if strings.TrimSpace(*nodeName) == "" {
		exitUsageError(out, "缺少必填参数 --name", "node run-loop 需要 node 名", "dalek node run-loop --name node-c")
	}
	if strings.TrimSpace(*verifyTarget) != "" && *runID != 0 {
		exitUsageError(out, "参数冲突", "--run-id 与 --verify-target 不能同时指定", "选择 inspect 现有 run 或提交新的 run")
	}
	if strings.TrimSpace(*manifestFile) != "" && strings.TrimSpace(*workspaceDir) != "" {
		exitUsageError(out, "参数冲突", "--manifest-file 与 --workspace-dir 不能同时指定", "选择现成 manifest 或从工作区生成")
	}
	if (strings.TrimSpace(*manifestFile) != "" || strings.TrimSpace(*workspaceDir) != "") && strings.TrimSpace(*snapshotID) == "" {
		exitUsageError(out, "缺少必填参数 --snapshot-id", "--manifest-file/--workspace-dir 需要配合 --snapshot-id 使用", "例如: dalek node run-loop --name node-c --snapshot-id snap-88 --workspace-dir .")
	}
	if err := requirePositiveInt("heartbeat-count", *heartbeatCount); err != nil {
		exitUsageError(out, "非法参数 --heartbeat-count", err.Error(), "例如: dalek node run-loop --heartbeat-count 1")
	}
	if *heartbeatInterval < 0 {
		exitUsageError(out, "非法参数 --heartbeat-interval", "--heartbeat-interval 不能为负值", "例如: dalek node run-loop --heartbeat-interval 1s")
	}
	if err := requirePositiveDuration("poll-interval", *pollInterval); err != nil {
		exitUsageError(out, "非法参数 --poll-interval", err.Error(), "例如: dalek node run-loop --poll-interval 1s")
	}
	if err := requirePositiveDuration("timeout", *timeout); err != nil {
		exitUsageError(out, "非法参数 --timeout", err.Error(), "例如: dalek node run-loop --timeout 10s")
	}
	if strings.TrimSpace(*watchStage) != "" {
		if err := requirePositiveDuration("stage-interval", *stageInterval); err != nil {
			exitUsageError(out, "非法参数 --stage-interval", err.Error(), "例如: dalek node run-loop --stage-interval 5s")
		}
	}

	project := mustOpenProjectWithOutput(out, *home, *proj)
	homeDir, err := app.ResolveHomeDir(*home)
	if err != nil {
		exitRuntimeError(out, "解析 --home 失败", err.Error(), "指定有效 Home 目录或设置 DALEK_HOME")
	}
	h, err := app.OpenHome(homeDir)
	if err != nil {
		exitRuntimeError(out, "打开 Home 失败", err.Error(), "检查 Home 目录权限与文件完整性")
	}
	if err := app.EnsureHomeSecrets(h); err != nil {
		exitRuntimeError(out, "初始化 Home secrets 失败", err.Error(), "检查 Home 目录权限后重试")
	}
	cfg := h.Config.WithDefaults()
	token := strings.TrimSpace(cfg.Daemon.Internal.NodeAgentToken)
	if token == "" {
		exitRuntimeError(out, "node agent token 未初始化", "daemon.internal.node_agent_token 为空", "先运行一次 dalek daemon start 生成内置 token")
	}
	client, err := nodeagentsvc.NewClient(nodeagentsvc.ClientConfig{
		BaseURL:   "http://" + strings.TrimSpace(cfg.Daemon.Internal.Listen),
		AuthToken: token,
		Timeout:   *timeout,
	})
	if err != nil {
		exitRuntimeError(out, "创建 node agent client 失败", err.Error(), "检查 daemon.internal.listen 配置后重试")
	}
	loop, err := nodeagentsvc.NewWorkerLoop(client, nodeagentsvc.WorkerLoopConfig{
		ProjectKey:       strings.TrimSpace(project.Name()),
		NodeName:         strings.TrimSpace(*nodeName),
		Endpoint:         strings.TrimSpace(*endpoint),
		ProtocolVersion:  nodeagentsvc.ProtocolVersionV1,
		RoleCapabilities: []string{"run"},
		ProviderModes:    []string{"run_executor"},
		SessionAffinity:  "run",
		DefaultProvider:  "run_executor",
	})
	if err != nil {
		exitRuntimeError(out, "创建 node worker loop 失败", err.Error(), "检查 node 参数后重试")
	}

	ctx, cancel := projectCtx(*timeout)
	defer cancel()
	loopResult, err := loop.RunHeartbeatLoop(ctx, nodeagentsvc.HeartbeatLoopConfig{
		Count:    *heartbeatCount,
		Interval: *heartbeatInterval,
	})
	if err != nil {
		if ctx.Err() != nil {
			exitRuntimeError(out, "node run-loop 超时", ctx.Err().Error(), "增大 --timeout 或减小 --heartbeat-count")
		}
		exitRuntimeError(out, "node register 失败", err.Error(), "确认 daemon 已启动且 internal listen 可访问")
	}
	if !loopResult.Register.Accepted {
		exitRuntimeError(out, "node register 未被接受", "register response accepted=false", "检查 node 配置和 daemon 校验逻辑")
	}
	heartbeatResp := loopResult.LastHeartbeat

	var uploadRes nodeagentsvc.SnapshotUploadResponse
	manifestJSON := ""
	if strings.TrimSpace(*workspaceDir) != "" {
		if strings.TrimSpace(*baseCommit) == "" {
			detectedBaseCommit, err := resolveGitHEAD(strings.TrimSpace(*workspaceDir))
			if err != nil {
				exitRuntimeError(out, "解析 base commit 失败", err.Error(), "显式传入 --base-commit 或确认工作区是有效 git 仓库")
			}
			*baseCommit = detectedBaseCommit
		}
		if strings.TrimSpace(*workspaceGeneration) == "" {
			*workspaceGeneration = strings.TrimSpace(*snapshotID)
		}
		manifest, err := snapshotsvc.BuildManifestFromWorkspace(snapshotsvc.BuildManifestInput{
			WorkspaceDir:        strings.TrimSpace(*workspaceDir),
			BaseCommit:          strings.TrimSpace(*baseCommit),
			WorkspaceGeneration: strings.TrimSpace(*workspaceGeneration),
		})
		if err != nil {
			exitRuntimeError(out, "生成 workspace manifest 失败", err.Error(), "检查 --workspace-dir、--base-commit 和 --workspace-generation")
		}
		_, manifestJSON, err = snapshotsvc.ComputeManifestDigest(manifest)
		if err != nil {
			exitRuntimeError(out, "序列化 workspace manifest 失败", err.Error(), "检查工作区文件与 snapshot 参数后重试")
		}
	} else if strings.TrimSpace(*manifestFile) != "" {
		raw, err := os.ReadFile(strings.TrimSpace(*manifestFile))
		if err != nil {
			exitRuntimeError(out, "读取 manifest 文件失败", err.Error(), "确认 --manifest-file 路径存在且可读")
		}
		manifestJSON = strings.TrimSpace(string(raw))
	}
	if strings.TrimSpace(manifestJSON) != "" {
		uploadRes, err = loop.UploadSnapshot(ctx, nodeagentsvc.UploadSnapshotInput{
			SnapshotID:          strings.TrimSpace(*snapshotID),
			BaseCommit:          strings.TrimSpace(*baseCommit),
			WorkspaceGeneration: strings.TrimSpace(*workspaceGeneration),
			ManifestJSON:        manifestJSON,
		})
		if err != nil {
			exitRuntimeError(out, "node upload snapshot 失败", err.Error(), "确认 manifest 内容合法且 daemon internal API 可访问")
		}
		if strings.TrimSpace(uploadRes.SnapshotID) != "" {
			*snapshotID = strings.TrimSpace(uploadRes.SnapshotID)
		}
		if strings.TrimSpace(uploadRes.BaseCommit) != "" && strings.TrimSpace(*baseCommit) == "" {
			*baseCommit = strings.TrimSpace(uploadRes.BaseCommit)
		}
	}

	var submitRes nodeagentsvc.RunSubmitResponse
	var inspect nodeagentsvc.InspectRunResult
	pollCount := 0
	waitHeartbeatCount := 0
	stageWatchCount := 0
	if strings.TrimSpace(*verifyTarget) != "" {
		if *waitForTerminal {
			submitInspect, err := loop.SubmitWaitAndInspectRun(ctx, nodeagentsvc.SubmitRunInput{
				RequestID:    strings.TrimSpace(*requestID),
				VerifyTarget: strings.TrimSpace(*verifyTarget),
				SnapshotID:   strings.TrimSpace(*snapshotID),
				BaseCommit:   strings.TrimSpace(*baseCommit),
			}, nodeagentsvc.WaitConfig{
				PollInterval:      *pollInterval,
				HeartbeatInterval: *heartbeatInterval,
			}, 20)
			if err != nil {
				exitRuntimeError(out, "node submit run 失败", err.Error(), "确认 verify target、snapshot 和 daemon internal API 可访问")
			}
			submitRes = submitInspect.Submission
			inspect = submitInspect.Inspect
			pollCount = submitInspect.PollCount
			waitHeartbeatCount = submitInspect.HeartbeatCount
		} else {
			submitInspect, err := loop.SubmitAndInspectRun(ctx, nodeagentsvc.SubmitRunInput{
				RequestID:    strings.TrimSpace(*requestID),
				VerifyTarget: strings.TrimSpace(*verifyTarget),
				SnapshotID:   strings.TrimSpace(*snapshotID),
				BaseCommit:   strings.TrimSpace(*baseCommit),
			}, 20)
			if err != nil {
				exitRuntimeError(out, "node submit run 失败", err.Error(), "确认 verify target、snapshot 和 daemon internal API 可访问")
			}
			submitRes = submitInspect.Submission
			inspect = submitInspect.Inspect
		}
	} else if *runID != 0 {
		if *waitForTerminal {
			inspect, pollCount, waitHeartbeatCount, err = loop.WaitForRunTerminal(ctx, uint(*runID), nodeagentsvc.WaitConfig{
				PollInterval:      *pollInterval,
				HeartbeatInterval: *heartbeatInterval,
			}, 20)
			if err != nil {
				exitRuntimeError(out, "node wait run 失败", err.Error(), "确认 run_id 存在且 daemon internal API 可访问")
			}
		} else {
			inspect, err = loop.InspectRun(ctx, uint(*runID), 20)
			if err != nil {
				exitRuntimeError(out, "node inspect run 失败", err.Error(), "确认 run_id 存在且 daemon internal API 可访问")
			}
		}
	}

	initialStage := strings.TrimSpace(inspect.Run.LifecycleStage)
	hadRecoveryStage := strings.EqualFold(initialStage, "recovery")
	if strings.TrimSpace(*watchStage) != "" && strings.EqualFold(initialStage, strings.TrimSpace(*watchStage)) {
		res, err := loop.WaitForStageChange(ctx, inspect.Run.RunID, strings.TrimSpace(*watchStage), nodeagentsvc.WaitConfig{
			PollInterval:      *stageInterval,
			HeartbeatInterval: *heartbeatInterval,
		})
		if err != nil {
			exitRuntimeError(out, "node stage watch 失败", err.Error(), "确认 run_id 可访问且 stage 正确")
		}
		stageWatchCount = res.PollCount
		waitHeartbeatCount += res.HeartbeatCount
		inspect = res.Inspect
	}

	if out == outputJSON {
		warnings := buildNodeRunLoopWarnings(inspect)
		if hadRecoveryStage {
			warnings = append(warnings, "run 曾处于 recovery 阶段，请关注节点恢复与状态回补")
		}
		payload := map[string]any{
			"schema":               "dalek.node.run-loop.v1",
			"project":              strings.TrimSpace(project.Name()),
			"node_name":            strings.TrimSpace(*nodeName),
			"accepted":             loopResult.Register.Accepted,
			"session_epoch":        loop.SessionEpoch(),
			"heartbeat_count":      loopResult.HeartbeatCount,
			"heartbeat_at":         loop.LastHeartbeat(),
			"status":               strings.TrimSpace(heartbeatResp.Status),
			"poll_count":           pollCount,
			"wait_heartbeat_count": waitHeartbeatCount,
			"stage_watch_count":    stageWatchCount,
			"warnings":             warnings,
		}
		if *runID != 0 {
			payload["run"] = inspect.Run
			payload["logs"] = inspect.Logs
			payload["artifacts"] = inspect.Artifacts
		}
		if strings.TrimSpace(*verifyTarget) != "" {
			payload["submission"] = submitRes
			payload["run"] = inspect.Run
			payload["logs"] = inspect.Logs
			payload["artifacts"] = inspect.Artifacts
		}
		if hadRecoveryStage {
			payload["recovery_stage"] = true
		}
		if strings.TrimSpace(*manifestFile) != "" || strings.TrimSpace(*workspaceDir) != "" {
			payload["snapshot_upload"] = uploadRes
		}
		printJSONOrExit(payload)
		return
	}

	fmt.Printf("node loop ok: project=%s node=%s epoch=%d heartbeats=%d status=%s\n",
		strings.TrimSpace(project.Name()),
		strings.TrimSpace(*nodeName),
		loop.SessionEpoch(),
		loopResult.HeartbeatCount,
		strings.TrimSpace(heartbeatResp.Status),
	)
	if strings.TrimSpace(*verifyTarget) != "" {
		fmt.Printf("submitted run=%d request_id=%s status=%s target=%s\n",
			submitRes.RunID,
			strings.TrimSpace(submitRes.RequestID),
			strings.TrimSpace(submitRes.Status),
			strings.TrimSpace(*verifyTarget),
		)
	}
	if strings.TrimSpace(*manifestFile) != "" || strings.TrimSpace(*workspaceDir) != "" {
		fmt.Printf("snapshot uploaded: snapshot=%s status=%s digest=%s\n",
			strings.TrimSpace(uploadRes.SnapshotID),
			strings.TrimSpace(uploadRes.Status),
			strings.TrimSpace(uploadRes.ManifestDigest),
		)
	}
	if (*runID != 0 || strings.TrimSpace(*verifyTarget) != "") && inspect.Run.Found {
		fmt.Printf("run=%d status=%s stage=%s summary=%s artifacts=%d polls=%d wait_heartbeats=%d\n",
			inspect.Run.RunID,
			strings.TrimSpace(inspect.Run.Status),
			strings.TrimSpace(inspect.Run.LifecycleStage),
			strings.TrimSpace(inspect.Run.Summary),
			len(inspect.Artifacts.Artifacts),
			pollCount,
			waitHeartbeatCount,
		)
		if stageWatchCount > 0 {
			fmt.Printf("watching stage '%s' changed after %d polls\n",
				strings.TrimSpace(*watchStage), stageWatchCount)
		}
		if strings.TrimSpace(inspect.Run.LifecycleStage) == "recovery" {
			fmt.Printf("run=%d is in recovery; wait for key node to reconnect or rerun after fix\n", inspect.Run.RunID)
		}
		if strings.TrimSpace(inspect.Run.LastEventType) == "run_artifact_upload_failed" {
			fmt.Printf("run=%d artifact upload partially failed; execution status remains %s\n",
				inspect.Run.RunID,
				strings.TrimSpace(inspect.Run.Status),
			)
		}
	}
}

func buildNodeRunLoopWarnings(inspect nodeagentsvc.InspectRunResult) []string {
	out := make([]string, 0, 2)
	if strings.TrimSpace(inspect.Run.LifecycleStage) == "recovery" {
		out = append(out, "run 仍处于 recovery 阶段，等待关键节点恢复或修复后重试")
	}
	if strings.TrimSpace(inspect.Run.LastEventType) == "run_artifact_upload_failed" {
		out = append(out, fmt.Sprintf("artifact 上传部分失败，但执行状态保持为 %s", strings.TrimSpace(inspect.Run.Status)))
	}
	return out
}

func resolveGitHEAD(workspaceDir string) (string, error) {
	workspaceDir = strings.TrimSpace(workspaceDir)
	if workspaceDir == "" {
		return "", fmt.Errorf("workspace_dir 不能为空")
	}
	out, err := exec.Command("git", "-C", workspaceDir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	head := strings.TrimSpace(string(out))
	if head == "" {
		return "", fmt.Errorf("git rev-parse 返回空值")
	}
	return head, nil
}
