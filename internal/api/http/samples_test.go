package httpapi_test

import (
	"testing"
	"time"
)

// TestProjectSamples: the samples route draws the rows hostd sends, as
// fractions of the class, with pressure unknown (null) for a row from a
// guest that predates I-493, and the busiest process names (I-492).
func TestProjectSamples(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-sam", "sam")
	e.subscribe(t, "sub-sam", "")
	if _, err := e.h.Pool.Exec(ctx, "update users set billing_status = 'exempt' where logto_sub = 'sub-sam'"); err != nil {
		t.Fatal(err)
	}
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "busy", "class": "large"})
	if r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	id := r.body["id"].(string)
	e.waitOp(t, r)
	if _, err := e.h.Pool.Exec(ctx, "delete from meter_samples where project_id = $1", id); err != nil {
		t.Fatal(err)
	}

	// Two minutes in one bucket: a large (4 vCPU) machine at full CPU for
	// one minute and half for the next; the newer row carries pressure,
	// the older one predates I-493 (mem_used 0). A third, stopped, row and
	// a row older than the window are left out.
	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	ins := func(ts time.Time, state string, cpuNs, memUsed, pressure, wait, disk int64) {
		t.Helper()
		if _, err := e.h.Pool.Exec(ctx, `insert into meter_samples (ts, project_id, state, class, cpu_ns, mem_used, cpu_pressure_us, host_cpu_wait_us, disk_used)
			values ($1, $2, $3, 'large', $4, $5, $6, $7, $8)`, ts, id, state, cpuNs, memUsed, pressure, wait, disk); err != nil {
			t.Fatal(err)
		}
	}
	ins(base.Add(5*time.Second), "running", 240e9, 0, 0, 6e6, 1<<30)
	ins(base.Add(50*time.Second), "running", 120e9, 2<<30, 30e6, 0, 2<<30)
	ins(base.Add(55*time.Second), "stopped", 999e9, 9<<30, 60e6, 60e6, 9<<30)
	ins(time.Now().Add(-2*time.Hour), "running", 240e9, 1<<30, 60e6, 60e6, 1<<30)
	for _, p := range []struct {
		comm string
		cpu  int64
		rss  int64
		ago  time.Duration
	}{{"node", 200e9, 1 << 30, 0}, {"cc1plus", 300e9, 2 << 20, 0}, {"node", 150e9, 3 << 30, 10 * time.Second}, {"old", 900e9, 1, 3 * time.Hour}} {
		if _, err := e.h.Pool.Exec(ctx, "insert into proc_samples (ts, project_id, comm, cpu_ns, rss) values ($1, $2, $3, $4, $5)",
			time.Now().Add(-p.ago-time.Minute), id, p.comm, p.cpu, p.rss); err != nil {
			t.Fatal(err)
		}
	}

	r = e.do(t, tok, "GET", "/projects/"+id+"/samples", nil)
	if r.status != 200 {
		t.Fatalf("samples: %d %s", r.status, r.raw)
	}
	if r.body["window"] != "1h" || r.body["step_s"].(float64) != 60 || r.body["vcpus"].(float64) != 4 || r.body["memory_bytes"].(float64) != 8<<30 {
		t.Fatalf("header: %s", r.raw)
	}
	pts := r.body["points"].([]any)
	if len(pts) != 1 {
		t.Fatalf("points: %s", r.raw)
	}
	pt := pts[0].(map[string]any)
	if pt["cpu"].(float64) != 0.75 {
		t.Errorf("cpu %v, want 0.75", pt["cpu"])
	}
	if pt["mem_used_bytes"].(float64) != 2<<30 {
		t.Errorf("mem_used_bytes %v, want only the row that reported it", pt["mem_used_bytes"])
	}
	if pt["cpu_pressure"].(float64) != 0.5 {
		t.Errorf("cpu_pressure %v, want 0.5 over the one reporting row", pt["cpu_pressure"])
	}
	if pt["host_cpu_wait"].(float64) != 0.05 {
		t.Errorf("host_cpu_wait %v, want 0.05", pt["host_cpu_wait"])
	}
	if pt["disk_used_bytes"].(float64) != 2<<30 {
		t.Errorf("disk_used_bytes %v", pt["disk_used_bytes"])
	}
	procs := r.body["procs"].([]any)
	if len(procs) != 2 {
		t.Fatalf("procs: %v", procs)
	}
	first := procs[0].(map[string]any)
	if first["comm"] != "node" || first["cpu_s"].(float64) != 350 || first["rss_max_bytes"].(float64) != 3<<30 {
		t.Fatalf("node not summed over the window: %v", procs)
	}

	// Only old rows: pressure is null, never a false zero.
	if _, err := e.h.Pool.Exec(ctx, "update meter_samples set mem_used = 0 where project_id = $1", id); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, tok, "GET", "/projects/"+id+"/samples?window=24h", nil)
	if r.status != 200 || r.body["step_s"].(float64) != 300 {
		t.Fatalf("24h: %d %s", r.status, r.raw)
	}
	for _, p := range r.body["points"].([]any) {
		if v := p.(map[string]any)["cpu_pressure"]; v != nil {
			t.Fatalf("pressure from rows without it: %v", v)
		}
	}

	if r := e.do(t, tok, "GET", "/projects/"+id+"/samples?window=2y", nil); r.status != 400 || errCode(r) != "invalid" {
		t.Fatalf("bad window: %d %s", r.status, r.raw)
	}
	other := e.signIn(t, "sub-eve", "eve")
	if r := e.do(t, other, "GET", "/projects/"+id+"/samples", nil); r.status != 404 {
		t.Fatalf("another user's project: %d %s", r.status, r.raw)
	}
}

// TestProjectCarriesRootFilesystem: GET /projects/:id carries the guest's
// root filesystem from the newest sample beside disk_used_bytes, the
// volume's allocated figure, and leaves it out when the sample has none
// (a guest older than I-567).
func TestProjectCarriesRootFilesystem(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-root", "rooted")
	e.subscribe(t, "sub-root", "")
	if _, err := e.h.Pool.Exec(ctx, "update users set billing_status = 'exempt' where logto_sub = 'sub-root'"); err != nil {
		t.Fatal(err)
	}
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "full", "class": "large"})
	if r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	id := r.body["id"].(string)
	e.waitOp(t, r)
	// A minute ahead, so a sample the fake host sends meanwhile is older.
	ts := time.Now().UTC().Add(time.Minute)
	if _, err := e.h.Pool.Exec(ctx, `insert into meter_samples (ts, project_id, state, class, disk_used, root_used, root_size)
		values ($1, $2, 'running', 'large', $3, $4, $5)`, ts, id, int64(39500)<<20, int64(33)<<30, int64(39)<<30); err != nil {
		t.Fatal(err)
	}
	g := e.do(t, tok, "GET", "/projects/"+id, nil)
	if g.body["root_used_bytes"] != float64(int64(33)<<30) || g.body["root_size_bytes"] != float64(int64(39)<<30) {
		t.Fatalf("root %v of %v: %s", g.body["root_used_bytes"], g.body["root_size_bytes"], g.raw)
	}
	if g.body["disk_used_bytes"] != float64(int64(39500)<<20) {
		t.Fatalf("disk_used_bytes %v, want the allocated figure", g.body["disk_used_bytes"])
	}
	if _, err := e.h.Pool.Exec(ctx, `insert into meter_samples (ts, project_id, state, class, disk_used)
		values ($1, $2, 'running', 'large', $3)`, ts.Add(time.Minute), id, int64(1)<<30); err != nil {
		t.Fatal(err)
	}
	g = e.do(t, tok, "GET", "/projects/"+id, nil)
	if _, ok := g.body["root_size_bytes"]; ok {
		t.Fatalf("root_size_bytes from a sample without it: %s", g.raw)
	}
}
