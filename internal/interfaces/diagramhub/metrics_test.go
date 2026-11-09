package diagramhub

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/topic"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/yjs"
)

func val(c prometheus.Metric) float64 {
	var m dto.Metric
	_ = c.Write(&m)
	return m.GetCounter().GetValue()
}

func TestYjsMetricsCountUpdatesAndBatches(t *testing.T) {
	mem := store(t, "m")
	_, b := fixture(t, mem, Options{})
	in0, out0 := val(yjsIn), val(yjsOut)
	ups := updates(t, "two clients")

	b.from(alice, topic.YjsUpTopic("m", alice), yjs.Update(ups[0]))
	b.from(bob, topic.YjsUpTopic("m", bob), yjs.Update(ups[0])) // duplicate: not counted
	eventually(t, "the batch write", func() bool { return len(stored(mem, "m").Updates) == 1 })

	if d := val(yjsIn) - in0; d != 1 {
		t.Errorf("updates in +%v, want 1", d)
	}
	if d := val(yjsOut) - out0; d != 1 {
		t.Errorf("updates out +%v, want 1", d)
	}
	var m dto.Metric
	_ = yjsFlush.Write(&m)
	if m.GetHistogram().GetSampleCount() == 0 {
		t.Error("no batch write latency recorded")
	}
}
