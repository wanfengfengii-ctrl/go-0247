package api

import "net/http"

// index serves the single-page operator UI. The page only carries out the
// documented field operations through the same JSON API; it performs no
// business adjudication of its own.
func (h *Handler) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeJSON(w, http.StatusNotFound, Envelope{Error: &ErrorBody{Code: "NOT_FOUND", Message: "no such route"}})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(operatorPage))
}

const operatorPage = `<!doctype html>
<html lang="zh">
<head>
<meta charset="utf-8">
<title>TorqueChain 高强螺栓连接检验</title>
<style>
body{font-family:system-ui,sans-serif;max-width:900px;margin:2rem auto;padding:0 1rem;color:#1a1a1a}
h1{font-size:1.4rem} form{border:1px solid #ccc;border-radius:6px;padding:1rem;margin:1rem 0}
label{display:block;margin:.4rem 0} input{width:100%;padding:.3rem;box-sizing:border-box}
button{margin-top:.6rem;padding:.4rem 1rem} pre{background:#f5f5f5;padding:.8rem;overflow:auto}
</style>
</head>
<body>
<h1>TorqueChain 高强螺栓连接检验与交工签收</h1>
<p>以下表单通过同一 JSON API 完成现场操作；业务裁定全部由服务端领域组件执行。</p>

<form id="lock">
<h2>锁定任务</h2>
<label>task_id<input name="task_id" value="T-1"></label>
<label>generation<input name="generation" value="1"></label>
<label>operation_no<input name="operation_no" value="op-lock-1"></label>
<label>node_ids (逗号分隔)<input name="node_ids" value="N-01,N-02"></label>
<label>device_id<input name="device_id" value="TD-001"></label>
<label>design_preload_n<input name="design_preload_n" value="240000"></label>
<label>torque_range_nm (min,max)<input name="torque_range" value="300,700"></label>
<label>angle_range_deg (min,max)<input name="angle_range" value="30,360"></label>
<label>trials<input name="trials" value="3"></label>
<button type="submit">锁定</button>
</form>

<form id="generic">
<h2>通用操作</h2>
<label>路径<select name="path">
<option>/v1/tasks/T-1/pair-verifications</option>
<option>/v1/tasks/T-1/torque-rechecks</option>
<option>/v1/tasks/T-1/tightening/initial</option>
<option>/v1/tasks/T-1/tightening/final</option>
<option>/v1/tasks/T-1/sampling-results</option>
<option>/v1/tasks/T-1/leases/claim</option>
<option>/v1/tasks/T-1/leases/release</option>
<option>/v1/tasks/T-1/reviews</option>
<option>/v1/tasks/T-1/finalize/sign</option>
<option>/v1/tasks/T-1/finalize/quarantine</option>
<option>/v1/tasks/T-1/finalize/cancel</option>
</select></label>
<label>JSON body<textarea name="body" rows="6">{"operation_no":"op-1","operator_id":"P-ALICE","task_revision":1,"node_id":"N-01","bolt_no":1,"bolt_batch":"B-001","nut_batch":"N-001","washer_batch":"W-001","grade":"10.9","socket_spec":"M24"}</textarea></label>
<button type="submit">提交 POST</button>
</form>

<pre id="out">响应将显示在这里</pre>
<script>
async function post(url, body){
  const r = await fetch(url, {method:'POST', headers:{'Content-Type':'application/json'}, body});
  return {status:r.status, json:await r.json()};
}
function show(x){ document.getElementById('out').textContent = JSON.stringify(x, null, 2); }

document.getElementById('lock').addEventListener('submit', async e=>{
  e.preventDefault();
  const f = new FormData(e.target);
  const [tmin,tmax] = f.get('torque_range').split(',').map(Number);
  const [amin,amax] = f.get('angle_range').split(',').map(Number);
  const body = {
    task_id: f.get('task_id'), generation: Number(f.get('generation')),
    operation_no: f.get('operation_no'), node_ids: f.get('node_ids').split(',').map(s=>s.trim()),
    device_id: f.get('device_id'),
    torque_bounds: {design_preload_n:Number(f.get('design_preload_n')), torque_range_nm:{min:tmin,max:tmax}, angle_range_deg:{min:amin,max:amax}},
    recheck_spec: {trials:Number(f.get('trials')), coefficient_min:0.08, coefficient_max:0.20},
    window: {start_unix:0, end_unix:4102444800}
  };
  show(await post('/v1/tasks', JSON.stringify(body)));
});

document.getElementById('generic').addEventListener('submit', async e=>{
  e.preventDefault();
  const f = new FormData(e.target);
  show(await post(f.get('path'), f.get('body')));
});
</script>
</body>
</html>`
