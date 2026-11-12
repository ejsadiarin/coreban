package config

import "testing"

func TestByteSizesReadAsAnOperatorWritesThem(t *testing.T) {
	for in, want := range map[string]int64{
		"5GB": 5 << 30, "5gb": 5 << 30, "500MB": 500 << 20, "64KB": 64 << 10, "1048576": 1 << 20, " 2 GB ": 2 << 30,
		"": 0, "0": 0, "-1GB": 0, "five": 0, "5TB": 0, "1.5GB": 0,
	} {
		if got := byteSize(in); got != want {
			t.Errorf("byteSize(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestTheQuotaMattersOnlyWithABucket(t *testing.T) {
	c := Config{DatabaseURL: "postgres://x", ReminderCap: 1}
	if err := c.ValidateServe(); err != nil {
		t.Errorf("no bucket, unreadable quota: %v, want it ignored", err)
	}
	c.Bucket.Endpoint = "http://localhost:19070"
	if err := c.ValidateServe(); err == nil {
		t.Error("a bucket with an unreadable quota was accepted")
	}
	c.AttachmentQuota = 5 << 30
	if err := c.ValidateServe(); err != nil {
		t.Errorf("a bucket with a quota: %v", err)
	}
}
