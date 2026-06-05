package main

import (
	"testing"
	"time"
)

// Regression: a scoped check (single-node "test" button → RunCheck(nil,&id), or
// a per-subscription check) used to reap every OTHER DNS group's persistent
// mihomo instance, because Check reaped against only the current call's node
// set. That defeated the persistence the refactor exists for. Check must no
// longer reap; reaping is ReapAbsent's job, called only on a full check.
// Found by /ship pre-landing review on 2026-06-04.
// Report: .gstack/qa-reports/qa-report-nodepanel-2026-06-04.md
func TestScopedCheckDoesNotReapOtherGroups(t *testing.T) {
	runner, teardown := newFakeMihomoRunner(t, "ok")
	defer teardown()

	// Simulate another subscription's already-running persistent instance, in a
	// different DNS group than the nodes we are about to check. A zero-value
	// instance is "alive" (dead=false) and its stop() is a safe no-op.
	runner.mu.Lock()
	runner.instances["other-group"] = &mihomoInstance{}
	runner.mu.Unlock()

	// fakeNodes carry no _mihomo_dns, so they form the "" group. A check that
	// only covers that group must NOT reap "other-group".
	outcome := runner.Check(fakeNodes(1, 2), time.Second)
	if !outcome.EngineAvailable {
		t.Fatalf("engine should be available, got %+v", outcome)
	}

	runner.mu.Lock()
	_, other := runner.instances["other-group"]
	_, checked := runner.instances[""]
	runner.mu.Unlock()
	if !other {
		t.Error("scoped Check reaped another group's instance — reapInstances over-reap regression")
	}
	if !checked {
		t.Error("expected the checked group's instance to be created")
	}

	// ReapAbsent is the explicit full-set reap: a node set that no longer
	// contains "other-group" must drop it.
	runner.ReapAbsent(fakeNodes(1, 2))
	runner.mu.Lock()
	_, otherAfter := runner.instances["other-group"]
	_, checkedAfter := runner.instances[""]
	runner.mu.Unlock()
	if otherAfter {
		t.Error("ReapAbsent did not reap the group absent from the full node set")
	}
	if !checkedAfter {
		t.Error("ReapAbsent wrongly reaped the group still present in the node set")
	}
}
