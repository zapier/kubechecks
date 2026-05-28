package github_client

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-github/v74/github"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"

	"github.com/zapier/kubechecks/pkg/vcs"
)

// DownloadArchive returns the archive URL for downloading a repository at a specific commit
func (c *Client) DownloadArchive(ctx context.Context, pr vcs.PullRequest) (string, error) {
	ctx, span := tracer.Start(ctx, "DownloadArchive")
	defer span.End()

	// Retry configuration for waiting on GitHub to compute merge commit SHA
	rc := c.archiveRetry.withDefaults(10, 1*time.Second, 16*time.Second)

	var ghPR *github.PullRequest
	var err error
	backoff := rc.initialBackoff

	// Retry loop: GitHub needs time to compute merge_commit_sha after PR creation/update
	for attempt := 0; attempt <= rc.maxRetries; attempt++ {
		// Get PR details to find merge_commit_sha
		ghPR, _, err = c.googleClient.PullRequests.Get(ctx, pr.Owner, pr.Name, pr.CheckID)
		if err != nil {
			return "", errors.Wrap(err, "failed to get PR details")
		}

		// CRITICAL: Validate that GitHub has processed the latest commit
		// When a new commit is pushed, GitHub may return outdated merge_commit_sha from the previous commit.
		// We must verify:
		// 1. HEAD SHA matches the expected SHA from the webhook
		// 2. Merge commit SHA is available (non-empty)
		// 3. Mergeable is non-nil (GitHub has finished recomputing the merge)
		//    When Mergeable is nil, GitHub is still processing - merge_commit_sha may be STALE
		//    from the previous HEAD, even though head.sha already reflects the new commit.
		headSHAMatches := ghPR.Head != nil && ghPR.Head.SHA != nil && *ghPR.Head.SHA == pr.SHA
		mergeCommitAvailable := ghPR.MergeCommitSHA != nil && *ghPR.MergeCommitSHA != ""
		mergeComputed := ghPR.Mergeable != nil // nil means GitHub is still computing

		if headSHAMatches && mergeCommitAvailable && mergeComputed {
			// Success - merge commit SHA is ready AND corresponds to the current HEAD
			log.Debug().
				Caller().
				Str("repo", pr.FullName).
				Int("pr_number", pr.CheckID).
				Str("head_sha", pr.SHA).
				Str("merge_commit_sha", *ghPR.MergeCommitSHA).
				Bool("mergeable", *ghPR.Mergeable).
				Msg("merge commit SHA is current and ready")
			break
		}

		// If GitHub has finished computing and determined the PR is not mergeable
		// for the current HEAD, short-circuit instead of waiting through all backoff intervals.
		// We require headSHAMatches because if the API is still serving stale PR data,
		// Mergeable reflects the previous commit and may flip once the API catches up.
		if headSHAMatches && mergeComputed && !*ghPR.Mergeable {
			log.Warn().
				Caller().
				Str("repo", pr.FullName).
				Int("pr_number", pr.CheckID).
				Str("head_sha", pr.SHA).
				Msg("PR is not mergeable (has conflicts); stopping retries")
			return "", errors.New("PR is not mergeable (has conflicts)")
		}

		// If this is the last attempt, fail with detailed info
		if attempt == rc.maxRetries {
			var reason string
			if !headSHAMatches {
				apiHeadSHA := "nil"
				if ghPR.Head != nil && ghPR.Head.SHA != nil {
					apiHeadSHA = *ghPR.Head.SHA
				}
				reason = fmt.Sprintf("HEAD SHA mismatch (expected: %s, got: %s)", pr.SHA, apiHeadSHA)
			} else if !mergeComputed {
				reason = "GitHub still computing merge status (mergeable is nil)"
			} else if !mergeCommitAvailable {
				reason = "merge commit SHA not available"
			}

			log.Warn().
				Caller().
				Str("repo", pr.FullName).
				Int("pr_number", pr.CheckID).
				Int("attempts", attempt+1).
				Str("reason", reason).
				Msg("failed to get current merge commit SHA after retries")
			return "", fmt.Errorf("PR merge commit SHA not ready (may have conflicts or GitHub still processing): %s", reason)
		}

		// Wait before retrying (exponential backoff)
		log.Debug().
			Caller().
			Str("repo", pr.FullName).
			Int("pr_number", pr.CheckID).
			Int("attempt", attempt+1).
			Dur("backoff", backoff).
			Bool("head_sha_matches", headSHAMatches).
			Bool("merge_commit_available", mergeCommitAvailable).
			Bool("merge_computed", mergeComputed).
			Msg("merge commit SHA not yet current, retrying...")

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(backoff):
			// Exponential backoff with cap
			backoff *= 2
			if backoff > rc.maxBackoff {
				backoff = rc.maxBackoff
			}
		}
	}

	mergeCommitSHA := *ghPR.MergeCommitSHA

	// Use the REST API zipball endpoint rather than the public web /archive/{sha}.zip URL.
	// The web URL on github.com does not honor `Authorization: Bearer` for fine-grained
	// PATs or App installation tokens on private repos and returns 404. The API endpoint
	// honors all token types and 302s to a signed codeload URL on a different host;
	// Go's http.Client strips the Authorization header on that cross-host redirect by
	// default, so the signed URL (already authenticated via query string) is fetched
	// without leaking the token. See issue #525.
	//
	// Enterprise detection mirrors createHttpClient: GHE requires both VcsBaseUrl and
	// VcsUploadUrl to be set. VcsBaseUrl alone isn't a reliable signal because config
	// defaults it to "https://github.com" when unset, so checking it would route
	// cloud users into the enterprise branch and build a bogus
	// `https://github.com/repos/...` URL that 404s.
	var archiveURL string
	if c.cfg.VcsUploadUrl != "" && c.cfg.VcsBaseUrl != "" {
		// GitHub Enterprise: VcsBaseUrl is the API base (typically includes /api/v3).
		baseURL := strings.TrimSuffix(c.cfg.VcsBaseUrl, "/")
		archiveURL = fmt.Sprintf("%s/repos/%s/%s/zipball/%s", baseURL, pr.Owner, pr.Name, mergeCommitSHA)
	} else {
		// GitHub.com
		archiveURL = fmt.Sprintf("https://api.github.com/repos/%s/%s/zipball/%s", pr.Owner, pr.Name, mergeCommitSHA)
	}

	log.Debug().
		Caller().
		Str("repo", pr.FullName).
		Int("pr_number", pr.CheckID).
		Str("merge_commit_sha", mergeCommitSHA).
		Str("archive_url", archiveURL).
		Msg("generated archive URL")

	return archiveURL, nil
}
