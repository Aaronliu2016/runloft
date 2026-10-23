package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aaronliu2016/runloft/internal/gitops"
	"github.com/aaronliu2016/runloft/internal/projectconfig"
	"github.com/aaronliu2016/runloft/internal/runner"
	"github.com/aaronliu2016/runloft/internal/runtimecheck"
	"github.com/aaronliu2016/runloft/internal/workspace"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type operationResult struct {
	message   string
	err       error
	session   *runner.Session
	entryURL  string
	stopWeb   bool
	update    *gitops.UpdatePlan
	updateID  string
	projectID string
	addedID   string
}

var sensitiveURLParam = regexp.MustCompile(`(?i)(token|secret|password|credential|api[_-]?key|authorization|access[_-]?key|private[_-]?key|signing[_-]?key)`)

type app struct {
	mu               sync.Mutex
	win              *mygo.Window
	store            *workspace.Store
	git              gitops.Git
	hooksDirectory   string
	projects         []workspace.Project
	selected         string
	repoURL          string
	workspacePath    string
	status           string
	projectStatus    string
	logs             []runner.LogLine
	busy             bool
	busyLabel        string
	operationCancel  context.CancelFunc
	operationDone    chan struct{}
	quitRequested    bool
	session          *runner.Session
	sessionProjectID string
	web              *mygo.WebView
	webOrigin        *url.URL
	entryURL         string
	updatePlan       *gitops.UpdatePlan
	updatePlanID     string
}

func main() {
	var a *app
	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		if a == nil || a.quitRequested {
			return
		}
		if !a.busy && a.session == nil {
			return
		}
		e.PreventDefault()
		a.quitRequested = true
		cancel, done := a.operationCancel, a.operationDone
		session := a.session
		go func() {
			if cancel != nil {
				cancel()
			}
			if done != nil {
				<-done
			}
			mygo.RunOnMain(func() { session = a.session })
			if session != nil {
				ctx, cancelStop := context.WithTimeout(context.Background(), 9*time.Second)
				_ = session.Stop(ctx)
				cancelStop()
			}
			mygo.RunOnMain(func() {
				if a.web != nil {
					a.web.Destroy()
					a.web = nil
				}
				a.session = nil
				mygo.App.Quit()
			})
		}()
	})
	mygo.App.WhenReady(func() {
		dataDirectory, err := mygo.App.Path(mygo.PathUserData)
		if err != nil {
			mygo.Dialog.Error("RunLoft", "无法定位应用数据目录："+err.Error())
			mygo.App.Quit()
			return
		}
		hooksDirectory := filepath.Join(dataDirectory, "empty-git-hooks")
		if err := os.MkdirAll(hooksDirectory, 0o700); err != nil {
			mygo.Dialog.Error("RunLoft", "无法准备 Git hooks 安全目录："+err.Error())
			mygo.App.Quit()
			return
		}
		store, err := workspace.OpenStore(filepath.Join(dataDirectory, "workspace.json"))
		if err != nil {
			mygo.Dialog.Error("RunLoft", "无法读取工作区列表："+err.Error())
			mygo.App.Quit()
			return
		}
		a = &app{store: store, hooksDirectory: hooksDirectory, status: "选择项目，或添加一个本地目录。"}
		a.git = gitops.Git{HooksDirectory: hooksDirectory}
		a.projects = store.List()
		if len(a.projects) != 0 {
			a.selected = a.projects[0].ID
		}
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title: "RunLoft",
			Width: 1180, Height: 780, MinWidth: 760, MinHeight: 560,
			Content: ui.View(a.view),
		})
		a.win.OnClose(func(e *mygo.CloseEvent) {
			if a.session != nil || a.busy {
				e.PreventDefault()
				mygo.App.Quit()
			}
		})
		a.refreshStatus()
	})
	if err := mygo.App.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func (a *app) view(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Padding(14).Gap(10).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "RunLoft").FontSize(22).Bold()
			ui.Text(c, "本地 Web 项目桌面入口").TextColor(t.TextMuted)
			ui.Row(c).Grow(1)
			if a.busy {
				ui.Text(c, a.busyLabel).TextColor(t.TextMuted)
			}
		})
		ui.Row(c).Gap(12).Grow(1).AlignItems(ui.Stretch).Children(func() {
			a.sidebar(c)
			a.content(c)
		})
	})
}

func (a *app) sidebar(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(310).MinWidth(250).Gap(8).Padding(8).Border(1, t.Border).Children(func() {
		ui.Text(c, "项目工作区").Bold()
		ui.TextInput(c, &a.repoURL).Placeholder("Git HTTPS / SSH 地址").FillWidth()
		ui.TextInput(c, &a.workspacePath).Placeholder("克隆到此目录的子文件夹").FillWidth()
		ui.Row(c).Gap(6).Children(func() {
			if ui.Button(c, "选择目录").Clicked() && !a.busy {
				go a.chooseWorkspace()
			}
			if ui.Button(c, "克隆仓库").Clicked() && !a.busy {
				a.cloneProject()
			}
			if ui.Button(c, "添加本地目录").Clicked() && !a.busy {
				go a.addLocalProject()
			}
		})
		ui.Text(c, "已添加项目").Bold().Padding(8, 0, 0, 0)
		ui.Scroll(c).Grow(1).Children(func() {
			if len(a.projects) == 0 {
				ui.Text(c, "暂无项目").TextColor(t.TextMuted)
			}
			for _, project := range a.projects {
				selected := project.ID == a.selected
				row := ui.Row(c.Key("project-" + project.ID)).Gap(5).Padding(4).AlignItems(ui.Center)
				if selected {
					row.Background(t.Accent).TextColor(t.AccentText)
				}
				if row.Clicked() {
					if a.session != nil && project.ID != a.sessionProjectID {
						a.status = "请先停止当前项目，再切换到另一个工作区。"
						a.win.Invalidate()
						return
					}
					a.selected = project.ID
					a.updatePlan = nil
					a.refreshStatus()
					a.win.Invalidate()
				}
				row.Children(func() {
					ui.Text(c, project.Name).Grow(1).SingleLine()
					if ui.Button(c.Key("forget-"+project.ID), "移除").Disabled(a.busy || project.ID == a.sessionProjectID).Clicked() && !a.busy {
						go a.removeProject(project)
					}
				})
			}
		})
	})
}

func (a *app) content(c *ui.Context) {
	t := c.Theme()
	project, selected := a.selectedProject()
	ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			if selected {
				ui.Text(c, project.Name).FontSize(17).Bold().Grow(1)
			} else {
				ui.Text(c, "项目详情").FontSize(17).Bold().Grow(1)
			}
			if ui.Button(c, "刷新状态").Clicked() && selected && !a.busy {
				a.refreshStatus()
			}
			if ui.Button(c, "查看配置与命令").Clicked() && selected && !a.busy {
				go a.inspectConfig(project)
			}
			if ui.Button(c, "检查更新").Clicked() && selected && !a.busy {
				a.checkUpdates(project)
			}
			if a.updatePlan != nil && a.updatePlanID == a.selected && a.updatePlan.Behind > 0 && ui.Button(c, "应用快进更新").Clicked() && !a.busy {
				a.applyUpdate(project)
			}
		})
		ui.Row(c).Gap(6).Children(func() {
			if ui.PrimaryButton(c, "准备依赖").Clicked() && selected && !a.busy {
				a.prepareProject(project)
			}
			if a.session == nil {
				if ui.PrimaryButton(c, "启动项目").Clicked() && selected && !a.busy {
					a.startProject(project)
				}
			} else if ui.Button(c, "停止项目").Clicked() && !a.busy {
				a.stopProject()
			}
			if a.web != nil && ui.Button(c, "关闭页面").Clicked() {
				a.web.Destroy()
				a.web = nil
				a.win.Invalidate()
			}
		})
		if selected {
			ui.Text(c, project.Path).SingleLine().TextColor(t.TextMuted)
			if project.RemoteURL != "" {
				ui.Text(c, "来源："+project.RemoteURL).SingleLine().TextColor(t.TextMuted)
			}
		}
		ui.Text(c, a.status).TextColor(t.TextMuted)
		if a.web != nil {
			ui.WebView(c, a.web).Grow(1).MinHeight(200).Border(1, t.Border)
		} else {
			ui.Column(c).Grow(1).MinHeight(200).Border(1, t.Border).Padding(12).Children(func() {
				if selected {
					ui.Text(c, "工作区状态").Bold()
					ui.Text(c, a.projectSummary(project)).TextColor(t.TextMuted)
				}
				ui.Text(c, "运行日志").Bold().Padding(8, 0, 0, 0)
				ui.Scroll(c).Grow(1).Children(func() {
					if len(a.logs) == 0 {
						ui.Text(c, "操作日志会显示在这里。").TextColor(t.TextMuted)
					}
					start := max(0, len(a.logs)-120)
					for _, line := range a.logs[start:] {
						ui.Text(c, fmt.Sprintf("%s  %s", line.Stream, line.Message)).FontSize(12).SingleLine()
					}
				})
			})
		}
	})
}

func (a *app) projectSummary(project workspace.Project) string {
	if a.projectStatus == "" {
		return "正在读取 Git 状态…"
	}
	return a.projectStatus
}

func (a *app) selectedProject() (workspace.Project, bool) {
	for _, project := range a.projects {
		if project.ID == a.selected {
			return project, true
		}
	}
	return workspace.Project{}, false
}

func (a *app) chooseWorkspace() {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: "选择克隆目录", Directory: true, ButtonLabel: "选择"})
	if err != nil {
		a.setStatus("选择目录失败：" + err.Error())
		return
	}
	if len(paths) == 0 {
		return
	}
	mygo.RunOnMain(func() {
		a.workspacePath = paths[0]
		a.win.Invalidate()
	})
}

func (a *app) addLocalProject() {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: "选择本地 Git 工作区", Directory: true, ButtonLabel: "添加"})
	if err != nil || len(paths) == 0 {
		if err != nil {
			a.setStatus("选择项目失败：" + err.Error())
		}
		return
	}
	root := paths[0]
	name := filepath.Base(root)
	if config, err := projectconfig.Load(root); err == nil {
		name = config.Project.Name
	}
	remote := ""
	if value, err := a.git.OriginURL(context.Background(), root); err == nil {
		remote = value
	}
	project, err := a.store.AddProject(workspace.Project{Name: name, Path: root, RemoteURL: remote})
	if err != nil && !strings.Contains(err.Error(), "already registered") {
		a.setStatus("添加项目失败：" + err.Error())
		return
	}
	mygo.RunOnMain(func() {
		if err == nil {
			a.projects = a.store.List()
			a.selected = project.ID
			a.status = "本地工作区已登记。添加项目不会复制或修改其中的文件。"
		} else {
			a.status = "该工作区已经登记。"
		}
		a.win.Invalidate()
	})
}

func (a *app) cloneProject() {
	repository := strings.TrimSpace(a.repoURL)
	if err := gitops.ValidateRepositoryURL(repository); err != nil {
		a.status = "仓库地址无效：" + err.Error()
		a.win.Invalidate()
		return
	}
	name, err := gitops.RepositoryName(repository)
	if err != nil {
		a.status = err.Error()
		a.win.Invalidate()
		return
	}
	parent := strings.TrimSpace(a.workspacePath)
	if parent == "" {
		a.status = "请先选择克隆目录。"
		a.win.Invalidate()
		return
	}
	destination := filepath.Join(parent, name)
	a.launch("克隆仓库", func(ctx context.Context) operationResult {
		if err := a.git.Clone(ctx, repository, destination); err != nil {
			return operationResult{err: err}
		}
		project, err := a.store.AddProject(workspace.Project{Name: name, Path: destination, RemoteURL: repository})
		if err != nil {
			return operationResult{err: err}
		}
		return operationResult{message: "仓库已克隆并添加到项目列表。", addedID: project.ID}
	}, func(result operationResult) {
		if result.addedID != "" {
			a.projects = a.store.List()
			a.selected = result.addedID
			a.projectStatus = "正在读取 Git 状态…"
		}
	})
}

func (a *app) removeProject(project workspace.Project) {
	response, err := mygo.Dialog.Message(mygo.MessageOptions{
		Parent: a.win, Type: mygo.MessageQuestion, Title: "移除项目登记",
		Message: "从 RunLoft 列表移除此项目？",
		Detail:  "只移除 RunLoft 中的登记信息，不会删除项目工作区或代码。",
		Buttons: []string{"取消", "移除登记"}, DefaultButton: 0, CancelButton: 0,
	})
	if err != nil || response.Button != 1 {
		return
	}
	if err := a.store.RemoveProject(project.ID); err != nil {
		a.setStatus("移除项目登记失败：" + err.Error())
		return
	}
	mygo.RunOnMain(func() {
		a.projects = a.store.List()
		if a.selected == project.ID {
			a.selected = ""
			if len(a.projects) > 0 {
				a.selected = a.projects[0].ID
			}
		}
		a.status = "项目登记已移除，工作区文件仍保留。"
		a.win.Invalidate()
	})
}

func (a *app) refreshStatus() {
	project, ok := a.selectedProject()
	if !ok {
		return
	}
	a.launch("读取 Git 状态", func(ctx context.Context) operationResult {
		status, err := a.git.Status(ctx, project.Path)
		if err != nil {
			return operationResult{err: err}
		}
		dirty := "工作区干净"
		if !status.Clean() {
			dirty = fmt.Sprintf("%d 项本地改动", len(status.DirtyPaths))
		}
		branch := status.Branch
		if branch == "" {
			branch = "分离 HEAD"
		}
		return operationResult{message: fmt.Sprintf("分支 %s · %s · 提交 %.12s", branch, dirty, status.Commit)}
	}, func(result operationResult) {
		if result.err != nil {
			a.projectStatus = "Git 状态不可用：" + result.err.Error()
		} else {
			a.projectStatus = result.message
		}
	})
}

func (a *app) checkUpdates(project workspace.Project) {
	a.launch("检查远端更新", func(ctx context.Context) operationResult {
		plan, err := a.git.CheckUpdates(ctx, project.Path)
		if err != nil {
			return operationResult{err: err}
		}
		return operationResult{message: fmt.Sprintf("更新检查完成：本地领先 %d 个提交，远端领先 %d 个提交；目标 %.12s。", plan.Ahead, plan.Behind, plan.TargetCommit), update: &plan, updateID: project.ID}
	}, func(result operationResult) {
		a.updatePlan = result.update
		a.updatePlanID = result.updateID
	})
}

func (a *app) inspectConfig(project workspace.Project) {
	config, digest, err := a.reviewConfig(project)
	if err != nil {
		_, _ = mygo.Dialog.Message(mygo.MessageOptions{Parent: a.win, Type: mygo.MessageError, Title: "项目配置不可用", Message: err.Error()})
		return
	}
	approval := "尚未授权；首次执行会要求审查并确认。"
	if a.store.IsApproved(project.ID, digest) {
		approval = "当前代码提交与配置已授权；提交或配置变化后会重新要求确认。"
	}
	workingPath, _ := config.WorkingPath(project.Path)
	var lines []string
	lines = append(lines, "来源："+a.projectSource(project))
	lines = append(lines, "配置：.runloft.yaml · schema v"+strconv.Itoa(config.SchemaVersion))
	lines = append(lines, "工作目录："+workingPath)
	for _, check := range config.Runtime.Checks {
		lines = append(lines, fmt.Sprintf("运行时：%s (%s %s)", check.Name, check.Command, check.Constraint))
	}
	for index, command := range config.Commands.Prepare.Steps {
		argv, _ := config.ExpandCommand(command)
		lines = append(lines, fmt.Sprintf("准备 %d：%s", index+1, displayArgv(argv)))
	}
	argv, _ := config.ExpandCommand(config.Commands.Start)
	lines = append(lines, "启动："+displayArgv(argv))
	lines = append(lines, fmt.Sprintf("服务：http://%s:%d%s · 等待 %d 秒", config.Server.Host, config.Server.Port, config.Server.HealthPath, config.Server.ReadyTimeoutSeconds))
	lines = append(lines, approval)
	_, _ = mygo.Dialog.Message(mygo.MessageOptions{
		Parent: a.win, Type: mygo.MessageInfo, Title: "配置与命令审查",
		Message: "下面的命令会在本机运行；这里只展示配置，不会执行。",
		Detail:  strings.Join(lines, "\n"), Buttons: []string{"关闭"},
	})
}

func (a *app) applyUpdate(project workspace.Project) {
	plan := a.updatePlan
	if plan == nil || a.updatePlanID != project.ID || plan.Behind == 0 {
		a.status = "没有可应用的快进更新。"
		a.win.Invalidate()
		return
	}
	response, err := mygo.Dialog.Message(mygo.MessageOptions{
		Parent: a.win, Type: mygo.MessageQuestion, Title: "应用快进更新",
		Message: fmt.Sprintf("将工作区快进到提交 %.12s。", plan.TargetCommit),
		Detail:  "操作仅在工作区干净、当前 HEAD 和已检查的目标提交未变化时执行。",
		Buttons: []string{"取消", "应用快进更新"}, DefaultButton: 0, CancelButton: 0,
	})
	if err != nil || response.Button != 1 {
		return
	}
	a.launch("应用快进更新", func(ctx context.Context) operationResult {
		if err := a.git.FastForward(ctx, project.Path, *plan); err != nil {
			return operationResult{err: err}
		}
		return operationResult{message: "快进更新已应用。", updateID: project.ID}
	}, func(_ operationResult) {
		a.updatePlan = nil
	})
}

func (a *app) prepareProject(project workspace.Project) {
	a.launch("检查配置并准备依赖", func(ctx context.Context) operationResult {
		config, digest, err := a.reviewConfig(project)
		if err != nil {
			return operationResult{err: err}
		}
		if err := a.requireRuntime(ctx, config); err != nil {
			return operationResult{err: err}
		}
		if !a.authorize(project, config, digest) {
			return operationResult{message: "准备步骤已取消。"}
		}
		if err := a.revalidateApproval(project, digest); err != nil {
			return operationResult{err: err}
		}
		err = runner.RunPrepare(ctx, project.Path, config, a.logLine)
		if err != nil {
			return operationResult{err: err}
		}
		return operationResult{message: "准备步骤已完成。"}
	}, nil)
}

func (a *app) startProject(project workspace.Project) {
	a.launch("检查配置并启动项目", func(ctx context.Context) operationResult {
		config, digest, err := a.reviewConfig(project)
		if err != nil {
			return operationResult{err: err}
		}
		if err := a.requireRuntime(ctx, config); err != nil {
			return operationResult{err: err}
		}
		if !a.authorize(project, config, digest) {
			return operationResult{message: "启动已取消。"}
		}
		if err := a.revalidateApproval(project, digest); err != nil {
			return operationResult{err: err}
		}
		result, err := runner.Start(ctx, project.Path, config, a.logLine)
		if err != nil {
			return operationResult{err: err}
		}
		return operationResult{message: "服务已就绪。", session: result.Session, entryURL: result.EntryURL, projectID: project.ID}
	}, func(result operationResult) {
		if result.session != nil {
			a.attachSession(result.session, result.entryURL, result.projectID)
		}
	})
}

func (a *app) stopProject() {
	session := a.session
	if session == nil {
		return
	}
	a.launch("停止项目", func(ctx context.Context) operationResult {
		if err := session.Stop(ctx); err != nil {
			return operationResult{err: err}
		}
		return operationResult{message: "项目已停止。", stopWeb: true}
	}, func(result operationResult) {
		if result.stopWeb {
			a.detachSession()
		}
	})
}

func (a *app) reviewConfig(project workspace.Project) (projectconfig.Config, string, error) {
	config, err := projectconfig.Load(project.Path)
	if err != nil {
		return projectconfig.Config{}, "", fmt.Errorf("读取项目配置失败：%w", err)
	}
	repositoryStatus, err := a.git.Status(context.Background(), project.Path)
	if err != nil {
		return projectconfig.Config{}, "", fmt.Errorf("读取 Git 工作区状态失败：%w", err)
	}
	if !repositoryStatus.Clean() {
		return projectconfig.Config{}, "", fmt.Errorf("工作区有 %d 项未提交改动；请先提交这些改动，再审查并运行项目", len(repositoryStatus.DirtyPaths))
	}
	repository := a.projectSource(project)
	digest, err := config.ApprovalDigest(repository, repositoryStatus.Commit)
	if err != nil {
		return projectconfig.Config{}, "", err
	}
	return config, digest, nil
}

func (a *app) revalidateApproval(project workspace.Project, approvedDigest string) error {
	_, currentDigest, err := a.reviewConfig(project)
	if err != nil {
		return err
	}
	if currentDigest != approvedDigest || !a.store.IsApproved(project.ID, currentDigest) {
		return errors.New("项目提交或配置在审查后发生变化；请重新审查并授权")
	}
	return nil
}

func (a *app) projectSource(project workspace.Project) string {
	if project.RemoteURL != "" {
		return project.RemoteURL
	}
	if origin, err := a.git.OriginURL(context.Background(), project.Path); err == nil {
		return origin
	}
	return "local:" + filepath.Clean(project.Path)
}

func (a *app) requireRuntime(ctx context.Context, config projectconfig.Config) error {
	results := runtimecheck.Check(ctx, config.Runtime.Checks)
	var failures []string
	for _, result := range results {
		if result.State == runtimecheck.Available {
			a.logLine(runner.LogLine{At: time.Now().UTC(), Stream: "runtime", Message: fmt.Sprintf("%s %s: %s", result.Name, result.Version, result.State)})
		}
		if result.State != runtimecheck.Available {
			failures = append(failures, fmt.Sprintf("%s: %s (%s)", result.Name, result.Detail, result.Constraint))
		}
	}
	if len(failures) == 0 {
		return nil
	}
	message := strings.Join(failures, "\n")
	_, _ = mygo.Dialog.Message(mygo.MessageOptions{
		Parent: a.win, Type: mygo.MessageWarning, Title: "运行时检查未通过",
		Message: "安装或切换到符合项目要求的本机运行时后再试。",
		Detail:  message, Buttons: []string{"确定"},
	})
	return errors.New(message)
}

func (a *app) authorize(project workspace.Project, config projectconfig.Config, digest string) bool {
	if a.store.IsApproved(project.ID, digest) {
		return true
	}
	preview := commandPreview(project, config)
	response, err := mygo.Dialog.Message(mygo.MessageOptions{
		Parent: a.win, Type: mygo.MessageQuestion, Title: "审查并授权项目命令",
		Message: "首次执行、仓库提交或配置变化。请审查仓库命令及工作目录。",
		Detail:  preview,
		Buttons: []string{"取消", "授权此配置"}, DefaultButton: 0, CancelButton: 0,
	})
	if err != nil || response.Button != 1 {
		return false
	}
	if err := a.store.Approve(project.ID, digest); err != nil {
		a.setStatus("保存命令授权失败：" + err.Error())
		return false
	}
	return true
}

func commandPreview(project workspace.Project, config projectconfig.Config) string {
	workingPath, err := config.WorkingPath(project.Path)
	if err != nil {
		workingPath = project.Path
	}
	var lines []string
	lines = append(lines, "工作目录："+workingPath)
	for index, command := range config.Commands.Prepare.Steps {
		argv, _ := config.ExpandCommand(command)
		lines = append(lines, fmt.Sprintf("准备 %d：%s", index+1, displayArgv(argv)))
	}
	argv, _ := config.ExpandCommand(config.Commands.Start)
	lines = append(lines, "启动："+displayArgv(argv))
	lines = append(lines, fmt.Sprintf("本地服务：http://%s:%d%s", config.Server.Host, config.Server.Port, config.Server.HealthPath))
	lines = append(lines, "环境：子进程继承 RunLoft 的当前进程环境。敏感环境变量名（值不显示）："+sensitiveEnvironmentNames())
	return strings.Join(lines, "\n")
}

func sensitiveEnvironmentNames() string {
	var names []string
	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if ok && sensitiveURLParam.MatchString(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "未检测到常见敏感名称"
	}
	return strings.Join(names, ", ")
}

func displayArgv(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = strconv.Quote(arg)
	}
	return strings.Join(quoted, " ")
}

func (a *app) launch(label string, work func(context.Context) operationResult, apply func(operationResult)) {
	if a.busy {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	a.busy = true
	a.busyLabel = label + "…"
	a.operationCancel = cancel
	a.operationDone = done
	a.status = a.busyLabel
	a.win.Invalidate()
	go func() {
		result := work(ctx)
		mygo.RunOnMain(func() {
			cancel()
			a.busy = false
			a.busyLabel = ""
			a.operationCancel = nil
			a.operationDone = nil
			if result.err != nil {
				a.status = label + "失败：" + result.err.Error()
			} else if result.message != "" {
				a.status = result.message
			}
			if apply != nil {
				apply(result)
			}
			close(done)
			a.win.Invalidate()
		})
	}()
}

func (a *app) attachSession(session *runner.Session, entry, projectID string) {
	a.session, a.sessionProjectID = session, projectID
	parsed, err := url.Parse(entry)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() == "" {
		a.status = "服务就绪 URL 校验失败。"
		a.stopUnattachedSession(session)
		return
	}
	if err := mygo.App.ClearBrowsingData(); err != nil {
		a.status = "无法清理上次项目的 WebView 数据，已停止加载页面。"
		a.stopUnattachedSession(session)
		return
	}
	web, err := a.win.NewWebView(mygo.WebViewOptions{URL: entry})
	if err != nil {
		a.status = "无法创建项目页面：" + err.Error()
		a.stopUnattachedSession(session)
		return
	}
	a.web, a.webOrigin, a.entryURL = web, parsed, entry
	page := web.Page()
	page.OnWillNavigate(func(event *mygo.NavigateEvent) {
		next, err := url.Parse(event.URL)
		if err == nil && sameOrigin(a.webOrigin, next) {
			return
		}
		event.PreventDefault()
		if external, ok := safeExternalURL(event.URL); event.UserInitiated && ok {
			go mygo.Shell.OpenExternal(external)
		} else {
			mygo.RunOnMain(func() { a.status = "已拦截项目页面的跨域或非 HTTP 导航。"; a.win.Invalidate() })
		}
	})
	page.SetWindowOpenHandler(func(request mygo.WindowOpenRequest) *mygo.WindowOptions {
		external, ok := safeExternalURL(request.URL)
		if !ok {
			a.setStatus("已阻止项目页面打开不安全的新窗口（仅支持 HTTP/HTTPS）。")
			return nil
		}
		a.setStatus("项目页面请求打开新窗口；请在系统对话框中确认是否继续。")
		go a.confirmExternalURL(external)
		return nil
	})
	page.OnPageTitleUpdated(func(event *mygo.TitleEvent) { event.PreventDefault() })
	page.OnDidFailLoad(func(loadErr *mygo.LoadError) {
		mygo.RunOnMain(func() {
			a.status = fmt.Sprintf("项目页面加载失败（WebView 错误 %d）。", loadErr.Code)
			a.win.Invalidate()
		})
	})
	go func() {
		<-session.Done()
		mygo.RunOnMain(func() {
			if a.session != session {
				return
			}
			if err, _ := session.ExitError(); err != nil {
				a.status = "项目进程已退出（" + runnerExitDescription(err) + "）。"
			} else {
				a.status = "项目进程已退出。"
			}
			a.detachSession()
			a.win.Invalidate()
		})
	}()
	a.win.Invalidate()
}

func (a *app) stopUnattachedSession(session *runner.Session) {
	go func() {
		if err := session.Stop(context.Background()); err != nil {
			a.setStatus("停止未能打开页面的项目失败：" + err.Error())
			return
		}
		mygo.RunOnMain(func() {
			if a.session == session {
				a.detachSession()
				a.win.Invalidate()
			}
		})
	}()
}

func runnerExitDescription(err error) string {
	var exit interface{ ExitCode() int }
	if errors.As(err, &exit) {
		return fmt.Sprintf("退出码 %d", exit.ExitCode())
	}
	return "异常退出"
}

func sameOrigin(a, b *url.URL) bool {
	return a != nil && b != nil && b.Scheme == "http" && b.User == nil && strings.EqualFold(a.Host, b.Host)
}

func safeExternalURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}
	query := u.Query()
	for key := range query {
		if sensitiveURLParam.MatchString(key) {
			query.Del(key)
		}
	}
	u.RawQuery = query.Encode()
	u.Fragment = ""
	return u.String(), true
}

func (a *app) confirmExternalURL(target string) {
	detail := target
	if runes := []rune(detail); len(runes) > 512 {
		detail = string(runes[:511]) + "…"
	}
	result, err := mygo.Dialog.Message(mygo.MessageOptions{
		Parent:        a.win,
		Type:          mygo.MessageQuestion,
		Title:         "打开外部链接",
		Message:       "项目页面请求在系统浏览器中打开这个网址。只有在你信任该网站时继续。",
		Detail:        detail,
		Buttons:       []string{"取消", "在浏览器中打开"},
		DefaultButton: 0,
		CancelButton:  0,
	})
	if err != nil {
		a.setStatus("无法显示外部链接确认对话框：" + err.Error())
		return
	}
	if result.Button != 1 {
		a.setStatus("已取消打开外部链接。")
		return
	}
	if err := mygo.Shell.OpenExternal(target); err != nil {
		a.setStatus("无法在系统浏览器中打开链接：" + err.Error())
		return
	}
	a.setStatus("已在系统浏览器中打开外部链接。")
}

func (a *app) detachSession() {
	if a.web != nil {
		a.web.Destroy()
		a.web = nil
	}
	a.webOrigin = nil
	a.entryURL = ""
	a.session = nil
	a.sessionProjectID = ""
}

func (a *app) logLine(line runner.LogLine) {
	mygo.RunOnMain(func() {
		if len(a.logs) == 1000 {
			copy(a.logs, a.logs[1:])
			a.logs[len(a.logs)-1] = line
		} else {
			a.logs = append(a.logs, line)
		}
		if a.win != nil {
			a.win.Invalidate()
		}
	})
}

func (a *app) setStatus(message string) {
	mygo.RunOnMain(func() {
		a.status = message
		if a.win != nil {
			a.win.Invalidate()
		}
	})
}
