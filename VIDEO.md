# Demo video script — Console + GitOps (3–4 min, no audio)

**Audience:** Technical (platform engineers, SREs, architects).  
**Format:** On-screen text only (title cards + lower-thirds / bullet overlays). No voiceover.  
**Tooling:** Adobe Premiere Pro — place each **TITLE CARD** and **BULLETS** as text layers; cut between screen recordings as indicated.

**Prerequisite for recording:** Run the stack in **Kubernetes / GitOps mode** (e.g. kind per [README.md](README.md), `ORCHESTRATION_MODE=k8s`, GitOps repo + Argo CD if you show Git sync). If you only have Docker Compose, skip or shorten Git/GitOps segments and use the console’s POC banners honestly.

**Target length:** ~3:45. Trim Segment 6 or 7 to land at 3:00; expand pauses on title cards to reach 4:00.

---

## Title options (pick one for open + close)

1. **Unified Ingress Control Plane — Console & GitOps**
2. **Fleet Gateways on Kubernetes — UI + Git + Live Pods**
3. **Ingress PoC — From Console to Cluster (Envoy, Kong, Argo CD)**

**Subtitle line (optional, under title):** `Go · Envoy · Kong · Kubernetes · Argo CD · OpenTelemetry`

---

## Introduction (on-screen text, ~8 sec)

**Card text (2 lines max):**

This demo shows how operators manage Envoy/Kong **fleets** and **routes** from the web console, how changes flow through **GitOps** to the data-plane cluster, and **terminal proof** that gateway workloads are real Kubernetes pods scaled by the platform.

---

## Segment overview (Premiere timeline)

| # | Topic | ~Sec | Recording source |
|---|--------|------|-------------------|
| 0 | Open title | 5 | Title graphic |
| 1 | Architecture | 25 | Console → Architecture |
| 2 | Fleets + scale + pods | 55 | Console Fleets + terminal |
| 3 | Stop/start node + suspend fleet | 35 | Console Fleets |
| 4 | New Lambda route | 40 | Console Routes |
| 5 | GitOps — repo, commits, Argo | 45 | Console GitOps + browser Git + optional Argo |
| 6 | Drift + reconcile | 30 | Console Drift + terminal (optional split) |
| 7 | Traces | 18 | Console Traces → Jaeger |
| 8 | Close title | 5 | Title graphic |

**~4:18 total.** To hit **3:00**, drop Segment 6 or 7; to hit **3:30**, shorten 5 and 6.

---

## SEGMENT 0 — Open (5 sec)

**TITLE CARD**

`UNIFIED INGRESS CONTROL PLANE`  
`Console · GitOps · Kubernetes · Observability`

**Recording:** Static title or subtle zoom. No gameplay.

---

## SEGMENT 1 — Architecture (25 sec)

**TITLE CARD (3 sec)**

`REQUEST PATH — EDGE TO SERVICE`

**Bullets (show after title, over or below Architecture page):**

- Single diagram: mock edge → PSAAS → Envoy/Kong → auth (OPA) → backends
- Control plane vs data plane separation is explicit on this page

**Recording:** Console → **Architecture**. Slow scroll top → bottom once (~20 sec). Readable font size in capture (1080p minimum).

---

## SEGMENT 2 — Fleet management + live pod scale (55 sec)

**TITLE CARD (3 sec)**

`FLEET GATEWAYS = REAL PODS IN THE DATA PLANE`

**Bullets:**

- Fleets group Envoy and Kong gateway **nodes** per hostname / region
- Scaling node count updates the **Deployment** — not a mock counter

**Recording — split screen recommended:**  
**Left:** Console → **Fleets** → expand a data-plane fleet (e.g. JPMM-style fleet with Envoy + Kong counts). Show **Running Nodes** and instance/route summary.

**Right (terminal, same recording or PiP):**

Before you start the clip, run (adjust label selector to match your `Fleet` CR / pod labels, e.g. `app.kubernetes.io/part-of=fleet-jpmm` or labels from `kubectl get pods -n ingress-dp --show-labels`):

```bash
kubectl get pods -n ingress-dp --context kind-ingress-cp -w
```

Or a non-watching proof if `-w` is hard to fit in edit:

```bash
watch -n 1 'kubectl get pods -n ingress-dp -o wide --context kind-ingress-cp'
```

**Action on console:** Change **node count / scale** for that fleet (e.g. 2 → 3), save. **Terminal:** new pod(s) appear `Pending` → `Running`.

**Action 2:** Scale back (3 → 2). **Terminal:** pod `Terminating` → gone.

**Bullets (mid-segment overlay, 5 sec):**

- Console calls **management-api** → operator / GitOps → **Kubernetes API**
- `kubectl` proves the cluster state matches what the UI requested

---

## SEGMENT 3 — Stop / start node + fleet suspend (35 sec)

**TITLE CARD (3 sec)**

`LIFECYCLE — NODES AND FLEETS`

**Bullets:**

- **Node:** stop/start affects a single gateway pod (maintenance, drain)
- **Fleet:** suspend stops gateways and deactivates routes for that hostname

**Recording:** Console → **Fleets** → same expanded fleet.

1. Hover **stop** on one **running** gateway node; confirm. Show node going to stopped / exited (or equivalent status).
2. **Start** the same node; show **running** again.
3. Use **Suspend fleet** (or equivalent) from fleet actions; show status **suspended** and nodes/routes reflecting deactivation.
4. **Resume** fleet; show **healthy** / active again.

**Terminal (optional 10 sec B-roll):**

```bash
kubectl get pods -n ingress-dp --context kind-ingress-cp
```

Show pod count or names changing around suspend/resume if your operator removes pods on suspend.

---

## SEGMENT 4 — New Lambda-backed route (40 sec)

**TITLE CARD (3 sec)**

`ADD A ROUTE — LAMBDA RUNTIME`

**Bullets:**

- Route targets a **function** instead of a static backend URL
- Platform provisions the **lambda runtime** and wires the gateway to it

**Recording:** Console → **Routes** → **New Route** (or Deploy flow from Fleets if that is where lambda lives in your build).

1. Choose hostname + path (e.g. `/lambda-demo`).
2. Set destination type to **Lambda** / **function** per your UI.
3. Paste minimal sample handler or pick template; save / deploy.
4. Show the new route **active** in the list (and under fleet instances if visible).

**Optional terminal (if lambda runs as a pod in DP):**

```bash
kubectl get pods -n ingress-dp --context kind-ingress-cp | grep -i lambda
```

---

## SEGMENT 5 — GitOps: Git + Argo CD (45 sec)

**TITLE CARD (3 sec)**

`DESIRED STATE IN GIT — ARGO CD APPLIES TO THE CLUSTER`

**Bullets:**

- Console changes **commit** Fleet/Route manifests to the GitOps repo (in k8s mode)
- Argo CD **syncs** the data-plane cluster continuously — no manual `kubectl apply` in prod

**Recording A — Console:** **GitOps** page (`/gitops`). Show **mode: K8s / GitOps**, repo list or sync status, recent activity.

**Recording B — Git host:** Browser → your **GitHub/GitLab** repo → **Commits** → latest commit message/diff showing a **route or fleet manifest** change (time-correlated with a console save if possible).

**Recording C (optional, short):** Argo CD UI — Application **Synced** / **Healthy**, or diff view for one resource.

**Terminal (optional):**

```bash
kubectl logs deployment/argocd-application-controller -n argocd --context kind-ingress-cp --tail=20
```

(Only if it adds clarity; otherwise skip.)

---

## SEGMENT 6 — Drift detection + reconcile (30 sec)

**TITLE CARD (3 sec)**

`DRIFT — REGISTRY vs ACTUAL GATEWAY CONFIG`

**Bullets:**

- **Desired** state: DB + Git; **actual** state: probes from gateways
- **Reconcile** pushes correct config back when drift is detected

**Recording:** Console → **Drift** / **Drift Dashboard**. Show at least one drift row or GitOps drift card if configured.

**Action:** Click **Reconcile** on a drifted route (or fleet). Show drift clearing / status turning green.

**Terminal (optional split, 15 sec):**

```bash
kubectl logs deployment/envoy-control-plane -n ingress-cp --context kind-ingress-cp --tail=15
```

Look for xDS / snapshot / reconcile log lines after reconcile.

---

## SEGMENT 7 — Distributed trace (18 sec)

**TITLE CARD (3 sec)**

`END-TO-END TRACES — JAEGER`

**Bullets:**

- One trace spans edge → gateway → **OPA** → upstream service

**Recording:** Console → **Traces** → open **most recent** meaningful trace. Expand **waterfall**; highlight **auth / OPA** span or gateway hop.

---

## SEGMENT 8 — Close (5 sec)

**TITLE CARD**

`UNIFIED INGRESS CONTROL PLANE`

**Stack line (small):**  
`Go · Envoy · Kong · Postgres · Kubernetes`  
`GitOps · Argo CD · OpenTelemetry · OPA`

---

## Premiere Pro notes

- **Safe title area:** Keep text inside title-safe margins (90% scale on 1080p).
- **Readable overlays:** Sans-serif, 42–56 px for bullets on 1080p; high contrast (white on dark or vice versa).
- **Pacing:** 3–4 seconds minimum on any bullet block; faster cuts confuse without audio.
- **Cursor:** Large pointer or subtle highlight clicks for key actions.
- **No audio:** Add a **lower third** on first appearance: `Demo — no narration`.

---

## Checklist before export

- [ ] One full dry run; total runtime 3:00–4:00
- [ ] All browser zoom levels consistent (100% or 125%)
- [ ] Terminal font large enough (14–16 pt equivalent in capture)
- [ ] Blur or redact tokens, cookies, internal URLs if needed
