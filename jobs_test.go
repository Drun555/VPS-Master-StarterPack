package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestJobRedactsAndReportsWarnings(t *testing.T) {
	manager := NewJobManager()
	job, err := manager.Start("test", []string{"top-secret"}, func(reporter *JobReporter) error {
		reporter.Log("value=top-secret")
		reporter.Warning("recoverable")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForJob(t, job)
	snapshot := job.Snapshot()
	if snapshot.Status != "success_with_warnings" {
		t.Fatalf("unexpected status %s", snapshot.Status)
	}
	for _, event := range snapshot.Events {
		if strings.Contains(event.Message, "top-secret") {
			t.Fatal("secret leaked into job log")
		}
	}
}

func TestJobError(t *testing.T) {
	manager := NewJobManager()
	job, err := manager.Start("test", nil, func(_ *JobReporter) error { return errors.New("boom") })
	if err != nil {
		t.Fatal(err)
	}
	waitForJob(t, job)
	if snapshot := job.Snapshot(); snapshot.Status != "error" || snapshot.Error != "boom" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func waitForJob(t *testing.T, job *Job) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if job.Snapshot().Status != "running" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job did not finish")
}
