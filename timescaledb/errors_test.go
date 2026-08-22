package timescaledb

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestFailedValidationPrecedesDataSourceUse(t *testing.T) {
	from := time.Now().UTC()
	_, err := NewFrameQuery[testFrame](Relation{Name: "events"}, "time").
		Bucket(FixedInterval(0)).Measure(Count().As("count")).
		Between(from, from.Add(time.Hour)).Execute(context.Background(), nil)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if strings.Contains(err.Error(), "nil DataSource") {
		t.Fatalf("DataSource checked before builder validation: %v", err)
	}
	if err := NewHypertable(Relation{Name: "events"}, "time").
		HashDimension("series", -1).Apply(context.Background(), nil); err == nil {
		t.Fatal("expected hypertable validation error")
	} else if strings.Contains(err.Error(), "nil DataSource") {
		t.Fatalf("DataSource checked before builder validation: %v", err)
	}
}
