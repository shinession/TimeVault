package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"timevault/internal/catalog"
	"timevault/internal/repository"
)

const (
	maxProblems     = 100
	verifyTickEvery = 500 * time.Millisecond
)

// VerifyResult summarizes an integrity check of one recovery point.
type VerifyResult struct {
	Deep       bool
	ManifestOK bool
	Objects    int64
	Missing    int64
	Corrupt    int64
	Problems   []string
}

// OK is true when nothing was wrong.
func (r VerifyResult) OK() bool { return r.ManifestOK && r.Missing == 0 && r.Corrupt == 0 }

func (r *VerifyResult) problem(format string, args ...any) {
	if len(r.Problems) < maxProblems {
		r.Problems = append(r.Problems, fmt.Sprintf(format, args...))
	}
}

// Verify checks that a committed point is still complete: the catalog still
// produces the digest recorded at commit time and every object it references
// exists with the right size. A deep check also re-reads and re-hashes each
// object once. A successful deep check marks the point "verified"; any problem
// marks it "failed", which blocks restores from it.
func (e *Engine) Verify(ctx context.Context, pointID int64, deep bool, rep Reporter) (VerifyResult, error) {
	if rep == nil {
		rep = NopReporter{}
	}
	res := VerifyResult{Deep: deep}
	p, err := e.cat.GetPoint(ctx, pointID)
	if err != nil {
		return res, err
	}
	if p.State != catalog.StateCommitted {
		return res, errors.New("backup: only committed recovery points can be verified")
	}
	rep.Phase(PhaseVerifying)

	sum, err := e.cat.ComputeManifest(ctx, p.SourceID, p.Seq)
	if err != nil {
		return res, err
	}
	res.ManifestOK = bytes.Equal(sum, p.ManifestSHA256)
	if !res.ManifestOK {
		res.problem("the catalog no longer matches the manifest digest recorded when the point was committed")
	}

	root, err := e.cat.RootNode(ctx, p.SourceID)
	if err != nil {
		return res, err
	}
	checked := map[repository.Hash]struct{}{}
	last := time.Now()
	err = e.cat.WalkAt(ctx, root, p.Seq, "", func(rel string, en catalog.Entry) error {
		if en.IsDir || en.State != catalog.VersionOK {
			return nil
		}
		h, err := repository.HashFromBytes(en.SHA256)
		if err != nil {
			res.Corrupt++
			res.problem("%s: invalid content hash in the catalog", rel)
			return nil
		}
		if _, done := checked[h]; done {
			return nil
		}
		checked[h] = struct{}{}
		res.Objects++
		if deep {
			err = e.repo.Verify(ctx, h, en.Size)
		} else {
			var size int64
			if size, err = e.repo.Stat(h); err == nil && size != en.Size {
				err = fmt.Errorf("%w: %s has %d bytes, expected %d", repository.ErrCorrupt, h, size, en.Size)
			}
		}
		switch {
		case err == nil:
		case errors.Is(err, repository.ErrMissing):
			res.Missing++
			res.problem("%s: object %s is missing from the repository", rel, h)
		case errors.Is(err, repository.ErrCorrupt):
			res.Corrupt++
			res.problem("%s: %v", rel, err)
		default:
			return err
		}
		if time.Since(last) > verifyTickEvery {
			last = time.Now()
			rep.Progress(Progress{Phase: PhaseVerifying, CurrentPath: rel, Files: res.Objects})
		}
		return nil
	})
	if err != nil {
		return res, err
	}

	status := ""
	switch {
	case !res.OK():
		status = catalog.VerificationFailed
	case deep:
		status = catalog.VerificationVerified
	}
	if status != "" {
		if err := e.cat.SetVerification(ctx, p.ID, status); err != nil {
			return res, err
		}
	}
	return res, nil
}
