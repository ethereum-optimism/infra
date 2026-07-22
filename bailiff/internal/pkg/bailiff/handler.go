package bailiff

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/ethereum/go-ethereum/log"
	"github.com/google/go-github/v66/github"
)

const (
	MaxCommentLen = 1024

	SHAPatternName = "sha"
)

var (
	ErrNoIssue          = errors.New("no issue found")
	ErrNotPullRequest   = errors.New("not a pull request")
	ErrNotCreation      = errors.New("not a creation event")
	ErrPRNotFound       = errors.New("pull request not found")
	ErrPRNotOpen        = errors.New("pull request not open")
	ErrPRFromUpstream   = errors.New("pull request from upstream repo")
	ErrNonWhitelisted   = errors.New("non-whitelisted user")
	ErrCommentTooLong   = errors.New("comment too long")
	ErrNoTriggerPattern = errors.New("no trigger pattern found")
	ErrMismatchedSHA    = errors.New("mismatched SHA")
)

var handlerExpectedErrors = []error{
	ErrNoIssue,
	ErrNotPullRequest,
	ErrNotCreation,
	ErrPRNotFound,
	ErrPRNotOpen,
	ErrPRFromUpstream,
	ErrNonWhitelisted,
	ErrCommentTooLong,
	ErrNoTriggerPattern,
	ErrMismatchedSHA,
}

type EventHandler struct {
	gh        *github.Client
	whitelist Whitelister
	config    *Config
	workdir   string
	lgr       log.Logger
	repusher  Repusher
}

type EventHandlerServer interface {
	ServeOnIssueComment(ctx context.Context, e *github.IssueCommentEvent) error
}

func NewEventHandler(gh *github.Client, whitelist Whitelister, config *Config, workdir string, lgr log.Logger, repusher Repusher) *EventHandler {
	return &EventHandler{
		gh:        gh,
		whitelist: whitelist,
		config:    config,
		workdir:   workdir,
		lgr:       lgr,
		repusher:  repusher,
	}
}

func (h *EventHandler) ServeOnIssueComment(ctx context.Context, e *github.IssueCommentEvent) error {
	err := h.OnIssueComment(ctx, e)
	RecordProcessedPR(err)
	if slices.Contains(handlerExpectedErrors, err) {
		return nil
	}
	return err
}

func (h *EventHandler) OnIssueComment(ctx context.Context, e *github.IssueCommentEvent) error {
	l := ReqIDLogger(ctx, h.lgr)

	issue := e.GetIssue()
	if issue == nil {
		l.Info("ignoring comment with no issue")
		return ErrNoIssue
	}

	if !issue.IsPullRequest() {
		l.Info("ignoring comment from non-PR source", "number", issue.GetNumber())
		return ErrNotPullRequest
	}

	prNum := issue.GetNumber()
	l = l.New("issue", prNum)

	if e.GetAction() != "created" {
		l.Info("ignoring comment with non-created action", "action", e.GetAction())
		return ErrNotCreation
	}

	pr, res, err := h.gh.PullRequests.Get(
		ctx,
		h.config.Org,
		h.config.Repo,
		issue.GetNumber(),
	)
	if err != nil {
		if res.StatusCode == 404 {
			l.Info("ignoring comment on non-existent PR")
			return ErrPRNotFound
		}

		return fmt.Errorf("failed to get pull request: %w", err)
	}

	if pr.GetState() != "open" {
		l.Info("ignoring comment on closed PR")
		return ErrPRNotOpen
	}

	forkRepo := pr.GetHead().GetRepo().GetFullName()
	if forkRepo == fmt.Sprintf("%s/%s", h.config.Org, h.config.Repo) {
		l.Info("ignoring comment on upstream repo PR")
		return ErrPRFromUpstream
	}

	if !h.whitelist.Whitelisted(e.GetSender().GetLogin()) {
		l.Info("ignoring comment from non-whitelisted user", "user", e.GetSender().GetLogin())
		return ErrNonWhitelisted
	}

	commentBody := e.GetComment().GetBody()
	if len(commentBody) > MaxCommentLen {
		l.Info("ignoring comment with too long body", "bodyLen", len(commentBody))
		return ErrCommentTooLong
	}

	requestedSHA := FindCommentSHA(h.config.TriggerPattern, commentBody)
	if requestedSHA == "" {
		l.Info("ignoring comment with non-matching trigger pattern")
		return ErrNoTriggerPattern
	}

	requestedSHA = strings.ToLower(requestedSHA)
	srcBranch := pr.GetHead().GetRef()
	upstreamBranch := FormatRepushBranch(forkRepo, srcBranch)
	headSHA := pr.GetHead().GetSHA()

	if headSHA != requestedSHA {
		l.Info("ignoring comment with mismatched SHA", "requestedSHA", requestedSHA, "headSHA", headSHA)
		return ErrMismatchedSHA
	}

	l.Info("repushing PR", "srcBranch", srcBranch, "upstreamBranch", upstreamBranch)
	if err := h.repusher.Repush(ctx, forkRepo, srcBranch, upstreamBranch, headSHA); err != nil {
		return fmt.Errorf("failed to repush: %w", err)
	}

	_, _, err = h.gh.Repositories.CreateStatus(ctx, h.config.Org, h.config.Repo, headSHA, &github.RepoStatus{
		Context:     github.String(h.config.StatusName),
		State:       github.String("success"),
		Description: github.String(fmt.Sprintf("Successfully repushed %s at %s.", srcBranch, headSHA)),
	})
	if err != nil {
		return fmt.Errorf("failed to create check run: %w", err)
	}

	return nil
}

func FormatRepushBranch(forkRepo, srcBranch string) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s/%s", forkRepo, srcBranch)))
	return fmt.Sprintf("external-fork/%x", hash)
}

func FindCommentSHA(re *regexp.Regexp, comment string) string {
	match := re.FindStringSubmatch(comment)

	if match == nil {
		return ""
	}

	// Find the index of the named group 'sha'
	groupNames := re.SubexpNames()
	for i, name := range groupNames {
		if name == SHAPatternName {
			return match[i]
		}
	}

	return ""
}
