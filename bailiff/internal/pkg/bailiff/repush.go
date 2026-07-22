package bailiff

import (
	"bufio"
	"context"
	_ "embed"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/log"
)

//go:embed repush.sh
var scriptSrc string

const maxAsyncRepushes = 10

type Repusher interface {
	Repush(ctx context.Context, forkRepo, srcBranch, upstreamBranch, requestedSHA string) error
}

type repushReq struct {
	forkRepo       string
	srcBranch      string
	upstreamBranch string
	requestedSHA   string
}

type AsnycRepusher struct {
	lgr      log.Logger
	queue    []repushReq
	repusher Repusher
	mtx      sync.Mutex
	doneC    chan struct{}
	closed   bool
	workers  int
}

func NewAsyncRepusher(lgr log.Logger, repusher Repusher) *AsnycRepusher {
	return &AsnycRepusher{
		lgr:      lgr,
		repusher: repusher,
		doneC:    make(chan struct{}),
	}
}

func (a *AsnycRepusher) Repush(ctx context.Context, forkRepo, srcBranch, upstreamBranch, requestedSHA string) error {
	a.mtx.Lock()
	defer a.mtx.Unlock()

	if a.closed {
		return fmt.Errorf("repusher is closed")
	}

	if len(a.queue) >= maxAsyncRepushes {
		return fmt.Errorf("queue is full")
	}

	a.queue = append(a.queue, repushReq{
		forkRepo:       forkRepo,
		srcBranch:      srcBranch,
		upstreamBranch: upstreamBranch,
		requestedSHA:   requestedSHA,
	})

	a.workers++
	go a.processQueue()

	return nil
}

func (a *AsnycRepusher) processQueue() {
	a.mtx.Lock()
	head := a.queue[0]
	a.queue = a.queue[1:]
	a.mtx.Unlock()

	defer func() {
		a.mtx.Lock()
		a.workers--
		if a.workers == 0 && a.closed {
			a.lgr.Info("all workers finished, shutting down")
			close(a.doneC)
		}
		a.mtx.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	lgr := a.lgr.New(
		"forkRepo", head.forkRepo,
		"srcBranch", head.srcBranch,
		"upstreamBranch", head.upstreamBranch,
		"requestedSHA", head.requestedSHA,
	)

	if err := a.repusher.Repush(ctx, head.forkRepo, head.srcBranch, head.upstreamBranch, head.requestedSHA); err != nil {
		lgr.Error(fmt.Sprintf("repush failed: %s", err))
		return
	}

	lgr.Info("repush succeeded")
}

func (a *AsnycRepusher) Close() {
	a.mtx.Lock()
	if a.closed {
		a.mtx.Unlock()
		return
	}
	a.closed = true
	a.mtx.Unlock()

	<-a.doneC
}

type ShellRepusher struct {
	lgr            log.Logger
	workdir        string
	privateKeyFile string
	mtx            sync.Mutex
}

func NewShellRepusher(lgr log.Logger, workdir string, privateKeyFile string) *ShellRepusher {
	return &ShellRepusher{
		lgr:            lgr,
		workdir:        workdir,
		privateKeyFile: privateKeyFile,
	}
}

func (s *ShellRepusher) Clone(ctx context.Context, repoURL string) error {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	env := fmt.Sprintf("GIT_SSH_COMMAND=ssh -i %s -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new", s.privateKeyFile)
	cmd := exec.CommandContext(
		ctx,
		"git",
		"clone",
		repoURL,
		".",
	)
	cmd.Dir = s.workdir
	cmd.Env = append(cmd.Env, env)

	doneC := make(chan struct{})
	if err := s.logOutput(cmd, doneC); err != nil {
		return fmt.Errorf("output logger setup failed: %s", err)
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command execution failed: %s", err)
	}

	<-doneC

	return nil
}

func (s *ShellRepusher) Repush(ctx context.Context, forkRepo, srcBranch, upstreamBranch, requestedSHA string) error {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	cmd := exec.CommandContext(
		ctx,
		"bash",
		"-c",
		scriptSrc,
		forkRepo,
		srcBranch,
		upstreamBranch,
		requestedSHA,
		s.privateKeyFile,
	)
	cmd.Dir = s.workdir

	doneC := make(chan struct{})
	if err := s.logOutput(cmd, doneC); err != nil {
		return fmt.Errorf("output logger setup failed: %s", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("command start failed: %s", err)
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("command execution failed: %s", err)
	}

	<-doneC

	return nil
}

func (s *ShellRepusher) logOutput(cmd *exec.Cmd, doneC chan struct{}) error {
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe creation failed: %s", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe creation failed: %s", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	outputLogger := func(r io.Reader, prefix string) {
		defer wg.Done()
		scan := bufio.NewScanner(r)
		for scan.Scan() {
			s.lgr.Info(fmt.Sprintf("[%s]: %s", prefix, scan.Text()))
		}
	}

	go outputLogger(stdoutPipe, "stdout")
	go outputLogger(stderrPipe, "stderr")
	go func() {
		wg.Wait()
		close(doneC)
	}()

	return nil
}
