package app

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// The install job.
//
// The work itself is behind the Installer interface: RealInstaller (install_real.go) builds and installs
// the mod, and DryRunInstaller (install_dry.go) only reads and checks the copies, for a choice the installer
// cannot install (ReadyToInstall). The job runs either one the same way: progress, Cancel and errors.

// Job states (InstallProgress.State).
const (
	JobIdle      = "idle"
	JobRunning   = "running"
	JobDone      = "done"
	JobCancelled = "cancelled"
	JobError     = "error"
)

// Phase ids (InstallProgress.Phase).
const (
	PhasePrepare = "prepare"
	PhaseGame    = "game"
	PhasePS3     = "ps3"
	PhaseX360    = "x360"
	PhaseFinish  = "finish"
)

// InstallProgress is what the UI shows while a job runs and after it ended. It is part of State, so it
// travels with every pushed state and carries the state's revision rules.
type InstallProgress struct {
	Job        int    `json:"job"`   // 1, 2, ... the number of the job this describes (0 = none yet)
	State      string `json:"state"` // idle | running | done | cancelled | error
	Phase      string `json:"phase"` // prepare | game | ps3 | x360 | finish ("" when idle)
	Message    string `json:"message"`
	DoneBytes  int64  `json:"doneBytes"`
	TotalBytes int64  `json:"totalBytes"`
	File       string `json:"file"` // the file being read, relative to its copy; never a full path
	Files      int    `json:"files"`
	FilesDone  int    `json:"filesDone"`
	Error      string `json:"error"`
	// Preview is true while the installer has no writer: the job reads and checks, nothing is written.
	Preview bool `json:"preview"`
}

// InstallRequest is everything an Installer needs; the Backend fills it from a plan that has no blockers.
type InstallRequest struct {
	Options Options // the items to install (the plan has been checked against these)
	GameDir string  // the F.E.A.R. folder
	PS3Path string  // "" unless the PlayStation 3 copy is used
	X360    string  // "" unless the Xbox 360 copy is used (the path of the folder or ISO)
}

// Installer does the work of a job. Run reports progress through report (Phase, Message, DoneBytes,
// TotalBytes, File, Files, FilesDone; the Backend adds Job and State), checks ctx between reads, and returns
// nil, ctx.Err() when cancelled, or an *InstallError (plain-language text) for anything else.
type Installer interface {
	Run(ctx context.Context, req InstallRequest, report func(InstallProgress)) error
}

// InstallError is a failure that can be shown to the person as it is.
type InstallError struct {
	Msg       string // one or two plain sentences
	Technical string // for a log or a tooltip, never the main message
}

func (e *InstallError) Error() string {
	if e.Technical != "" {
		return e.Msg + " (" + e.Technical + ")"
	}
	return e.Msg
}

// StartResult is the answer of StartInstall.
type StartResult struct {
	Started bool   `json:"started"`
	Job     int    `json:"job"`
	Reason  string `json:"reason"` // plain language, when Started is false
}

// job is the running job (guarded by Backend.mu).
type job struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// jobState is the part of Backend that belongs to the install job.
type jobState struct {
	installer    Installer
	cur          *job
	seq          int
	uninstalling bool // Uninstall is running: no job may start
}

// SetInstaller replaces the Installer (the program passes RealInstaller; tests pass fakes). It must not be
// called while a job runs.
func (b *Backend) SetInstaller(i Installer) {
	b.mu.Lock()
	b.job.installer = i
	b.mu.Unlock()
}

// StartInstall starts one install job and returns at once; progress arrives as pushed states
// (State.Install). It refuses when a job is already running, or when the plan for o has any blocker (a copy
// that is not verified, EchoPatch missing, nothing chosen, an unusable Default.archcfg, ...), so a job never
// starts from a stale or incomplete selection.
func (b *Backend) StartInstall(o Options) (res StartResult) {
	defer func() {
		if p := recover(); p != nil {
			res = StartResult{Reason: "The installation could not be started."}
		}
	}()
	for try := 0; try < 5; try++ {
		p := b.Plan(o)
		b.mu.Lock()
		if p.Rev != b.state.Rev {
			b.mu.Unlock() // the selection changed while the plan was computed: plan again
			continue
		}
		switch {
		case b.job.cur != nil:
			b.mu.Unlock()
			return StartResult{Reason: "An installation is already running."}
		case b.state.Dialog != "":
			b.mu.Unlock()
			return StartResult{Reason: "Close the file dialog first."}
		case b.job.uninstalling:
			b.mu.Unlock()
			return StartResult{Reason: "Wait for the uninstall to finish first."}
		case !p.CanContinue:
			b.mu.Unlock()
			reason := "Something is not ready yet."
			if len(p.Blockers) > 0 {
				reason = p.Blockers[0]
			}
			return StartResult{Reason: reason}
		}
		req := requestFor(b.state, o)
		inst := b.job.installer
		if inst == nil {
			// The real writer only when the installer can make every file of the choice; otherwise a check only.
			if ok, _ := ReadyToInstall(req); ok {
				inst = &RealInstaller{Log: b.logLine}
			} else {
				inst = NewDryRunInstaller()
			}
		}
		b.job.seq++
		id := b.job.seq
		ctx, cancel := context.WithCancel(context.Background())
		j := &job{cancel: cancel, done: make(chan struct{})}
		b.job.cur = j
		b.state.Install = InstallProgress{Job: id, State: JobRunning, Phase: PhasePrepare,
			Message: "Getting ready…", Preview: isPreview(inst)}
		b.touch()
		b.mu.Unlock()
		b.push()

		if isPreview(inst) {
			b.logf("Install %d started (preview: the copies are read and checked, nothing is written).", id)
		} else {
			b.logf("Install %d started.", id)
		}
		b.wg.Add(1)
		go b.runJob(ctx, j, id, inst, req)
		return StartResult{Started: true, Job: id}
	}
	return StartResult{Reason: "Your choices changed while the installer was checking them. Please try again."}
}

// previewer is implemented by an Installer that writes nothing.
type previewer interface{ IsPreview() bool }

func isPreview(i Installer) bool {
	if p, ok := i.(previewer); ok {
		return p.IsPreview()
	}
	return false
}

// runJob runs the installer in its own goroutine. A panic inside it becomes an error state, like the
// verification goroutines.
func (b *Backend) runJob(ctx context.Context, j *job, id int, inst Installer, req InstallRequest) {
	defer b.wg.Done()
	var last time.Time
	var lastPhase string
	report := func(p InstallProgress) {
		now := time.Now()
		b.mu.Lock()
		cur := &b.state.Install
		if b.job.cur != j || cur.Job != id || cur.State != JobRunning {
			b.mu.Unlock()
			return
		}
		keepMsg := cur.Message
		cur.Phase, cur.Message, cur.File = p.Phase, p.Message, p.File
		cur.Files, cur.FilesDone = p.Files, p.FilesDone
		if p.TotalBytes > 0 || p.DoneBytes == 0 {
			cur.TotalBytes = p.TotalBytes
		}
		if p.DoneBytes >= cur.DoneBytes { // progress never goes backwards
			cur.DoneBytes = p.DoneBytes
		}
		if cur.DoneBytes > cur.TotalBytes && cur.TotalBytes > 0 {
			cur.DoneBytes = cur.TotalBytes
		}
		if cur.Message == "" {
			cur.Message = keepMsg
		}
		// A change of phase is always pushed; byte progress at most every progressEvery.
		push := p.Phase != lastPhase || now.Sub(last) >= b.progressEvery
		if push {
			lastPhase, last = p.Phase, now
		}
		b.touch()
		b.mu.Unlock()
		if push {
			b.push()
		}
	}

	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = &InstallError{Msg: "The installer hit an unexpected problem and stopped. Nothing was changed.",
					Technical: fmt.Sprint("internal error: ", r)}
			}
		}()
		err = inst.Run(ctx, req, report)
	}()

	b.mu.Lock()
	cur := &b.state.Install
	if cur.Job == id {
		switch {
		case err == nil: // the work was finished, whether or not Cancel came too late to matter
			cur.State, cur.Phase, cur.Error = JobDone, PhaseFinish, ""
			cur.Message = "Done."
			if cur.Preview {
				cur.Message = "Check finished: your copies were read and checked, and nothing was written."
			}
			if cur.TotalBytes > 0 {
				cur.DoneBytes = cur.TotalBytes
			}
			cur.File = ""
		case errors.Is(err, context.Canceled):
			cur.State, cur.Error, cur.File = JobCancelled, "", ""
			cur.Message = "Cancelled. Nothing was changed."
		default:
			cur.State, cur.File = JobError, ""
			cur.Message = "The installation stopped."
			cur.Error = "The installer could not finish."
			var ie *InstallError
			if errors.As(err, &ie) {
				cur.Error = ie.Msg
			}
		}
	}
	b.job.cur = nil
	b.touch()
	b.mu.Unlock()
	switch {
	case err == nil:
		b.logf("Install %d finished.", id)
	case errors.Is(err, context.Canceled):
		b.logf("Install %d cancelled; nothing was changed.", id)
	default:
		b.logf("Install %d failed: %v", id, err)
	}
	if !isPreview(inst) {
		b.refreshPC() // the folder changed (or was put back): the hook state and ModInstalled follow it
	}
	close(j.done)
	b.push()
}

// CancelInstall asks the running job to stop; it returns at once and the job ends promptly (the installer
// checks for cancellation between reads). Without a running job it does nothing.
func (b *Backend) CancelInstall() State {
	b.mu.Lock()
	if j := b.job.cur; j != nil && b.state.Install.State == JobRunning {
		j.cancel()
		b.state.Install.Message = "Cancelling…"
		b.touch()
		b.mu.Unlock()
		b.push()
		return b.Snapshot()
	}
	b.mu.Unlock()
	return b.Snapshot()
}

// Shutdown cancels the install job and every running check and waits for them to stop. The host calls it
// when the window closes, so no goroutine is still reading a disc image when the program exits.
func (b *Backend) Shutdown() {
	b.mu.Lock()
	if j := b.job.cur; j != nil {
		j.cancel()
	}
	for _, c := range b.cancel {
		if c != nil {
			c()
		}
	}
	b.mu.Unlock()
	b.wg.Wait()
}

// requestFor is the InstallRequest a selection makes (Plan uses it to say whether INSTALL would really write).
func requestFor(st State, o Options) InstallRequest {
	req := InstallRequest{Options: o, GameDir: st.PC.Path}
	if o.UsePS3 {
		req.PS3Path = dataRoot(st.PS3)
	}
	if o.UseX360 {
		req.X360 = dataRoot(st.X360)
	}
	return req
}

// dataRoot is the folder the content build reads for a console copy: the one its check found (the player may
// have selected a folder above or inside it), else the selection itself (a disc image, or not checked yet; the
// install gate refuses those).
func dataRoot(s SourceState) string {
	if s.Result != nil && s.Result.Root != "" {
		return s.Result.Root
	}
	return s.Path
}
