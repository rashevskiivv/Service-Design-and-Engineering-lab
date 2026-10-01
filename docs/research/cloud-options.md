# Cloud options: a GPU for €0

Owner: Slava · Researcher: Cloud & model agent · **All facts checked 2026-09-30** against the source linked in [Sources](#sources) (`[Cx]`).
Anything marked **UNVERIFIED** was not confirmed on a primary page today and must be re-checked before we rely on it.
Prices are ex-VAT.

---

## TL;DR

| Rank | Path | Why | Main risk |
|---|---|---|---|
| **1 · Primary** | **OVHcloud Public Cloud free trial (€200, 1 month) → AI Deploy** with a vLLM container on **1× L40S 48 GB** (dev) / **1× H100 80 GB** (rehearsal + lab) | €200 covers ≈64 H100-h or ≈129 L40S-h, and we need ≈10–15 h. AI Deploy gives **4 GPUs per project by default** (no quota ticket), a **public HTTPS endpoint**, per-minute billing, and it is in France (GRA), so latency from Trento is low. [C60–C64] | Card required and **no hard spend cap found**, so any usage above €200 is charged to the card. Possible ID check of a new account (~48 h, anecdotal). The voucher is valid **1 month from project creation**. It is inferred, not stated, that the voucher covers AI Deploy. |
| **2 · Backup** | **Modal Starter ($30/month free)**: serverless GPU + the official vLLM example, `*.modal.run` HTTPS | Sign-up in minutes, no quota requests, and the **spend limit is a hard stop** [C4]. It includes 3 seats, so the team shares one workspace. | Only **$30/month**, which buys ≈6–7 H100-h all-in or ≈12 L40S-h, so dev must run on L4/A10. GPU containers are **preemptible** ("rare"). Web requests time out at **150 s** [C5, C6]. |
| 3 · Parallel long-shot | **Google Cloud $300 trial → upgrade to paid → Cloud Run GPU** (L4 24 GB or RTX PRO 6000 96 GB) | $300 is ≈285 L4-h. Cloud Run is one of the 4 services with a **real spend cap (preview)** [C12]. Docs say L4 quota is auto-granted on first deploy [C13]. | Trial accounts **cannot use GPUs** [C10]. The forums report the Cloud Run GPU auto-grant **not firing**, even on paid accounts (Jan 2026, Sep 2026) [C15, C16]. |
| 4 · Ask professor | Google Cloud **teaching credits** ($50/student + $100/staff, Italy eligible, ≤15 business days) and/or the **UniTrento HPC cluster** | Card-free. Legitimate for a course. | Approval may land after the lab date. HPC is batch + VPN, so serving a public API there is doubtful (UNVERIFIED). |
| **CPU fallback** | Slava's **MacBook M4 Pro** (laptop mode, `gpt-oss:20b` already pulled) or any 24 GB-RAM x86 box running llama.cpp with a 3B-active MoE model | €0, no sign-up | Throughput suits only a handful of concurrent users, so the rest queue. **Oracle Always Free A1 is now only 2 OCPU / 12 GB** [C50]. It was 4 OCPU / 24 GB in earlier years (from memory, not re-checked), so the brief's assumption is outdated. It can host the gateway but not a useful model. |

Not usable for our goal: Azure for Students (no GPU VMs), AWS free plan (no xlarge/GPU), GitHub Pack → DigitalOcean (**offer ended 31 Jul 2026**), Colab (ToS forbids web services), HF ZeroGPU (Gradio-only), Oracle trial GPUs (limit "Contact us").

---

## 1. What we need

| Phase | GPU-hours | Note |
|---|---|---|
| Dev / integration | ~5 h | Can run on a cheaper GPU (L4/L40S) |
| Rehearsal (load test with 30 clients) | ~2 h | Same GPU type as the lab |
| Lab session | ~3 h | Plus ~30 min warm-up (image pull + model download 30–40 GB + load) |
| **Total** | **~10 h, plan 15 h** | Buffer covers cold starts, a forgotten app, and a second rehearsal |

Hardware target: **≥ 48 GB VRAM** for the recommended model (Qwen3.6-35B-A3B FP8, ~37.5 GB of weights; see `model-choice.md`). 24 GB (L4) works with the smaller fallback model.

---

## 2. Comparison

"GPU-h for us" = free amount ÷ hourly price of the GPU we would use (my arithmetic).

| Option | GPU (VRAM) | Free amount | Card? | Quota / approval, typical wait | Public HTTP API allowed & possible? | GPU-h for us | Covers 10–15 h? |
|---|---|---|---|---|---|---|---|
| **OVHcloud AI Deploy** (trial voucher) | L4 24 GB €0.83/h · A10 24 GB €0.90/h · L40S 48 GB €1.55/h · A100 80 GB €3.00/h · H100 80 GB €3.10/h [C61] | **€200, 1 month** from first Public Cloud project; first-time Public Cloud customers only; one voucher per person [C60] | **Yes** ("valid payment method") [C60] | AI Deploy: "**maximum of 4 GPUs used simultaneously**" per project by default [C62]. New accounts: possible ID/billing-proof check, "around 48 hours" (anecdotal, 2023) [C68] | **Yes.** Each app gets an HTTPS URL `https://<uuid>.app.gra.ai.cloud.ovh.net`, **public** or **token-restricted** access, one exposed port, own Docker image [C62, C64] | L40S ≈129 h · H100 ≈64 h | **Yes** |
| **Modal** Starter | T4, L4 ($0.80/h), A10 ($1.10/h), L40S ($1.95/h), A100 40/80, H100 ($3.95/h), H200, B200 [C1] | **$30/month** compute; also academic grants "up to $10k" (application, timing unknown) [C1, C8] | **Yes**: "Using a GPU requires having a valid payment method on file" [C3] | None. Starter = 10 GPU concurrency [C1] | **Yes.** Web server endpoints; official vLLM example [C7]. **150 s max HTTP request** (then 303 redirect) [C6] | All-in (GPU + CPU $0.0000131/core/s + RAM $0.00000222/GiB/s): H100 ≈$4.4/h → ~6.8 h; L40S ≈$2.4/h → ~12 h; L4 ≈$1.25/h → ~24 h | **Just**, with mixed GPUs (dev on L4, lab on L40S/H100) |
| **Google Cloud** trial → paid | Cloud Run: **L4 24 GB** ($0.0001867/s ≈ $0.67/h, plus min 4 vCPU/16 GiB ≈ $0.38/h) or **RTX PRO 6000 96 GB** ($0.000365/s ≈ $1.31/h, plus min 20 vCPU/80 GiB ≈ $1.9/h); EU: europe-west1/west4 [C13, C14] | **$300, 90 days** [C10] | **Yes** [C10] | Trial: "can't add GPUs", "can't request a quota increase" [C10]. After upgrade, credit is kept. Cloud Run auto-grants 3 L4 / 3 RTX PRO 6000 "when the first deployment is created" [C13], but forum reports it stuck at 0 [C15, C16]. Compute Engine: `GPUS_ALL_REGIONS` starts at 0, request needed [C17] | **Yes** (Cloud Run gives HTTPS). Streaming works on Cloud Run (UNVERIFIED for our timeout) | L4 ≈285 h · RTX PRO 6000 ≈95 h | **Yes, if quota arrives** |
| Google Cloud **teaching credits** (professor applies) | Same as above | Up to **$50/student + $100/teaching staff**; Italy listed; redeem ≤16 weeks after course start [C18] | Not mentioned on the pages (education coupons normally need no card; UNVERIFIED) | "up to **15 business days**" to process [C18]; GPU quota on the edu billing account still to request (UNVERIFIED) | Yes | Plenty | **Timing risk** |
| **Azure for Students** | none usable | $100 / 12 months, no card [C20] | No | Community: **4 vCPU cap**, region policy [C22]. GPU needs upgrade to pay-as-you-go + quota request [C21]. Since Jul 2026 v1–v4 VM series (incl. NCasT4_v3) are capacity-restricted [C23] | n/a | **0** | **No** |
| **AWS** free plan | none on free plan | $100 + up to $100, 6 months [C30] | Yes (payment method at sign-up [C32], secondary source) | Free plan excludes "xlarge or above" instances [C32, secondary source, 2025], so no GPU. Paid plan keeps credits [C31], but G-instance vCPU quota for new accounts is typically 0 (UNVERIFIED) | Yes, technically | ~0 in time | **No** |
| GitHub Student Pack → **DigitalOcean** | — | **Ended.** DO left the pack; credits unavailable after 31 Jul 2026 [C40]. The pack page no longer lists DO [C41] | — | — | — | 0 | **No** |
| Other Student Pack items | — | Azure $100 (above), Heroku $13/mo, no GPU offers found [C41] | — | — | — | 0 | No |
| **Oracle Cloud** Free Tier | Always Free: **A1 Arm 2 OCPU / 12 GB** (1,500 OCPU-h + 9,000 GB-h per month) [C50] | Trial **US$300 for 30 days** [C51] | **Yes** (credit/debit that works like credit; no prepaid) [C51] | GPU shape limits = "**Contact Us**" (default 0) [C52]. Trial GPU approval unlikely (UNVERIFIED). Idle A1 instances can be reclaimed (<20% CPU/net/mem over 7 days) [C50] | Yes (public IP) | ~0 GPU | **No** for GPU; A1 can host the **gateway** |
| **Google Colab** free | T4 (not guaranteed) | Free | No | — | **No.** Free tier forbids "web service offerings not related to interactive compute", "remote proxies", and "bypassing the notebook UI to interact primarily via a web UI" [C70] | — | **No (ToS)** |
| **Kaggle** notebooks | P100 16 GB or 2× T4 | 30 GPU-h/week, 12 h sessions [C71, secondary] | No (phone verification needed for GPU/internet, UNVERIFIED) | — | Tunnelling a public API out of a notebook: Kaggle ToS pages are JS-rendered and **could not be read today (UNVERIFIED)**. Not built for it, no SLA, 2× T4 is weak | ≤30 h/week | **Not recommended** |
| **Hugging Face ZeroGPU** | ½ or full RTX PRO 6000 (48/96 GB) | Hosting free (account ≥30 days); **5 min/day GPU per free visitor** | No | — | **Gradio SDK only**, GPU allocated per function call [C72] | — | **No** |
| **Lightning AI** | T4…H100 | ~15 credits/month (secondary source) [C73] | UNVERIFIED | UNVERIFIED | UNVERIFIED (pages JS-rendered) | ~few h | UNVERIFIED |
| RunPod / Vast.ai / TensorDock | many | No free sign-up credits found. RunPod academic programme: case-by-case, reply in **7–10 business days** [C74] | Yes | — | Yes | 0 | No (timing) |
| NVIDIA Academic Grant | H100-hours | Faculty only; "**currently not accepting new applications**" [C77] | — | — | — | 0 | No |
| **UniTrento HPC** (`hpc2.unitn.it`) | UNVERIFIED (GPU nodes not confirmed) | Free for UniTN users | No | Access via professor/ICTS (KB article S00111 is login-only; robots-blocked). **VPN required** (GlobalProtect `vpn.icts.unitn.it`), PBS `qsub` batch jobs [C80] | Classmates could reach it only via VPN. A long-running public web service on batch nodes is very likely against policy (UNVERIFIED) | ? | **Ask** |

### Paid reference prices (not €0, for comparison)

| Provider | GPU | Price |
|---|---|---|
| OVHcloud GPU instances (GRA11) | L4 €0.75/h · L40S €1.40/h · H100 €2.80/h [C61] | New A100/H100/L4/L40S only in GRA11 [C67] |
| Scaleway | L4-1-24G €0.79/h; L40S/H100 in PAR-2 [C75] | No general free credits |
| Hetzner GEX45 | RTX PRO 4000 Blackwell 24 GB, **€214/month + €209 setup** (secondary source) [C76] | Monthly dedicated server, useless for 15 h |

---

## 3. Notes and gotchas per viable option

### OVHcloud AI Deploy (primary)
- **What it is:** managed containers on GPUs. You give a Docker image (Docker Hub is fine), a GPU flavor and a replica count, and you get an HTTPS endpoint. Billed **per minute** only while `SCALING`/`RUNNING`. A stopped app keeps its URL [C63].
- **Free trial:** €200, **1 month** after first project creation; "only valid for the purchase of Public Cloud services". The trial page's own examples include **35 h of AI Notebook AI1-1-GPU** and **5 h of AI Training on 4 GPUs** [C60]. It does not name AI Deploy explicitly, so AI Deploy coverage is **inferred**: confirm in the Control Panel under Billing → Credits after the first GPU minute.
- **Above €200 the card is charged** ("billed automatically for any future use") [C60]. **No hard spend cap** was found in the docs. **Stop and delete apps after every session** (`ovhai app stop <id>`).
- **Quota:** AI Deploy default = 4 GPUs at once per project [C62], so no ticket is needed. (Plain GPU *instances* may hit project quotas; manual quota upgrades are "billed immediately" as prepaid credit, and trial users "may have limited quota increase options" [C66]. Use **AI Deploy**, not instances.)
- **Container rules:** the container runs as **UID 42420, not root**. HOME and cache dirs must be `chown 42420:42420`. Image must be `linux/amd64`. **Only one port** (default 8080) [C62, C65]. So the vLLM image needs a 5-line wrapper Dockerfile (Jan). If the Go gateway runs in the same container, gateway on 8080 and vLLM on localhost.
- **Access control:** "Restricted access" expects `Authorization: Bearer <OVH AI token>` [C64], which **clashes with our own per-student Bearer keys**. Either (a) set the app to **public** and let our gateway (inside the container) do auth, or (b) keep it restricted and run the gateway elsewhere, injecting the OVH token upstream. Architect and Jan decide.
- **Hardware in GRA:** H200, H100, A100, A10, V100S, L4, L40S [C62]. **V100S is unusable with current vLLM** (needs compute capability ≥ 7.5) [C81].
- **Cold start:** local storage is ephemeral, so the model is re-downloaded on every start (~37 GB for our pick). Budget ~10 min. Alternatively attach Object Storage (billed; UNVERIFIED cost).
- **UNVERIFIED, test in week 1:** SSE streaming through the OVH ingress, ingress idle/request timeouts, H100 availability at the moment of the lab.

### Modal (backup)
- Card is mandatory for GPUs [C2, C3]. Set a **workspace spend limit**: "When the spend limit is reached, Modal stops workloads that would incur additional out-of-pocket charges" [C4].
- $30 is **per month**. October's $30 must cover dev + rehearsal + lab. Any September allowance ends with the month (inferred from "/ month"). If the lab slips into November, new credits arrive.
- **Preemption:** "All Modal Functions are subject to preemption by default … Preemptions are rare"; `nonpreemptible` is **not supported for GPU functions** [C5]. Mid-lab restarts are possible (low probability).
- **150 s HTTP timeout** on web functions [C6]. Streaming responses probably avoid it (UNVERIFIED). Keep `max_tokens` bounded; test a long non-streaming request.
- Scale-to-zero with a `scaledown_window` (example uses 15 min) [C7]. For the lab, pin 1 warm replica to avoid a multi-minute cold start.
- Region selection costs 1.15–1.75× [C1]. Use the default region.

### Google Cloud (parallel long-shot)
- Trial: "$300 in Welcome credit … 90 days". While in trial you cannot "Add GPUs to your VM instances" or "Request a quota increase". After **upgrading to paid**, "you keep any unused credit until it expires 90 days from the Free Trial signup", and "Your payment method is charged for any usage that exceeds your remaining credit" [C10].
- **Normal budgets do not cap spend** ("doesn't automatically cap … usage or spending") [C11]. **Spend-cap budgets (preview)** exist only for Gemini API, Vertex/Agent Platform, **Cloud Run** and Cloud Run functions. They pause the service at 100% and are computed on **gross cost (credits not subtracted)** [C12]. So a cap of e.g. $150 gross stays safely inside $300 of credit. **This makes Cloud Run the only GCP GPU path with a real cap.**
- Cloud Run GPU: min 4 vCPU / 16 GiB for L4, 20 vCPU / 80 GiB for RTX PRO 6000. Instance-based billing: "minimum instances are charged at the full rate even when idle" [C13]. RTX PRO 6000 (96 GB) is only in europe-west4 in the EU [C13].

### Microsoft / AWS / DigitalOcean / Oracle: why not
- **Azure for Students**: 4 vCPU cap and region policy [C22]. GPU requires upgrading to PAYG + quota request [C21]. Capacity restrictions on v1–v4 series since Jul 2026 [C23]. Its free B-series VMs (750 h) could host the Go gateway [C20].
- **AWS**: free plan excludes xlarge+ (all GPU types) [C32]. Paid plan keeps credits [C31], but G-quota approval for a brand-new account is slow/uncertain (UNVERIFIED). AWS Academy needs a UniTN educator-run class (not checked).
- **DigitalOcean via GitHub Pack**: ended; from 8 Jun 2026 it no longer covered GPU Droplets anyway (secondary source; the official GitHub post confirms the end date) [C40].
- **Oracle**: Always Free **A1 is 2 OCPU / 12 GB** today ("For Always Free tenancies, this is equivalent to 2 OCPUs and 12 GB of memory") [C50]. That fits the gateway + a ≤4B model at most. Trial $300/30 days, but GPU limits are "Contact Us" [C52].

---

## 4. Ranked recommendation

1. **Primary: OVHcloud trial → AI Deploy.** L40S (€1.55/h) for dev, H100 (€3.10/h) for rehearsal + lab. Expected spend ≈ 5×1.55 + 5×3.10 + buffer ≈ **€25–40 of the €200** voucher.
2. **Backup: Modal Starter** with a hard spend limit. The same vLLM command runs there, and the gateway's upstream URL is swapped by config (FR8). Budget: dev on L4 (~$6), rehearsal + lab 5 h on L40S (~$12) or H100 (~$22).
3. **Parallel, zero-cost to try: GCP trial**, upgraded to paid with a **Cloud Run spend cap**. It only helps if the Cloud Run L4/RTX PRO 6000 quota actually appears.
4. **Professor email (this week):** (a) UniTrento GPU/HPC access for a one-day demo, (b) Google Cloud teaching credits for the course (no card for students).
5. **CPU / laptop fallback:** laptop mode on the M4 Pro with Ollama `gpt-oss:20b` behind the same gateway, with a small queue and short `max_tokens`. For a Linux box, llama.cpp `llama-server` with `gpt-oss-20b` or `Qwen3-Coder-30B-A3B` Q4 (see `model-choice.md`). Tell classmates to expect waiting.

### Coverage of the ~10–15 GPU-h need

| Option | Covers? |
|---|---|
| OVH €200 | ✅ with >4× margin |
| Modal $30 | ⚠️ yes only with mixed GPUs; ~7 H100-h max |
| GCP $300 | ✅ if quota is granted, ❌ otherwise |
| Teaching credits | ✅ amount-wise, ⚠️ timing |
| Azure / AWS / DO / Oracle / Colab / ZeroGPU | ❌ |

---

## 5. Sign-up checklist (Slava does these himself; no one else enters card data)

### Start TODAY (Wed 30 Sep)
1. **OVHcloud (primary)**
   1. Create an OVHcloud account (Italy/EU site) with his own details; enable 2FA.
   2. Add a payment method (credit card or PayPal; accepted methods UNVERIFIED for IT).
   3. If OVH asks for ID/billing proof, upload immediately (anecdotally ~48 h) [C68].
   4. Create the **first Public Cloud project**. This starts the **€200 / 1-month** voucher (valid until ≈ 30 Oct). Check it under Billing → Credits/Vouchers; one forum user saw it missing and had to open a ticket (Sep 2026) [community post, see C60 note].
   5. In the project: AI → AI Deploy. Create an AI user + token, install the `ovhai` CLI.
   6. Smoke test (≈ €0.10): deploy the OVH hello-world image on **1× L4** for 5 minutes, curl it, then **stop + delete**. Confirm GPU quota and that the voucher is being consumed.
   7. Add a calendar reminder: "stop all AI Deploy apps" after every session.
2. **Modal (backup, 10 min):** sign up (GitHub login), add a card, and **set a workspace spend limit so out-of-pocket = $0**. Invite Jan (3 seats included). Do **not** run GPUs until needed, to save October credits.
3. **Email the professor** (Claude drafts): Google Cloud teaching credits + UniTrento GPU/HPC availability for one 3 h demo on ≈14–21 Oct.

### This week (by Fri 2 Oct)
4. **GCP (optional parallel path):** start the $300 trial, **upgrade to paid** immediately (credit is kept), create a project, set a **Cloud Run spend-cap budget** (e.g. $150 gross), and try a Cloud Run L4 deploy in `europe-west1`. If quota stays 0 for 48 h, drop the path.
5. Jan: build the vLLM wrapper image (UID 42420, port 8080) and test **SSE streaming + timeouts through the OVH endpoint** on an L4 (≈ €1).

### Next week (5–9 Oct)
6. Dev runs on OVH L40S (~5 h). Load test with 30 clients on H100 (~1 h) → numbers for Giulia.
7. Decide the final GPU (L40S vs H100) from the load test.

### Lab day
8. Start the app about 45 min before the lab. Warm up, run the smoke test, hand out keys. **Stop and delete the app right after the lab.** Check billing the next day.

---

## 6. Least-certain claims (re-check before relying)

1. The **OVH €200 voucher covers AI Deploy GPU usage.** Inferred from "Public Cloud services" plus the AI Notebook/Training examples. AI Deploy is not named explicitly.
2. **SSE streaming and timeouts** through OVH AI Deploy's ingress, and Modal streaming vs the 150 s limit: not documented; must be tested.
3. **UniTrento HPC** GPU hardware and whether a service may be exposed: not publicly documented (KB is login-only).
4. Kaggle ToS on tunnels, Lightning AI free credits, AWS default G quota: pages unreadable or not found.

---

## Sources

All checked 2026-09-30.

- [C1] Modal pricing: https://modal.com/pricing
- [C2] Modal billing: https://modal.com/docs/guide/billing
- [C3] Modal GPU guide: https://modal.com/docs/guide/gpu
- [C4] Modal budgets: https://modal.com/docs/guide/budgets
- [C5] Modal preemption: https://modal.com/docs/guide/preemption
- [C6] Modal web timeouts: https://modal.com/docs/guide/webhook-timeouts
- [C7] Modal vLLM example: https://modal.com/docs/examples/vllm_inference
- [C8] Modal academics: https://modal.com/academics
- [C10] GCP free trial: https://docs.cloud.google.com/free/docs/free-cloud-features
- [C11] GCP budgets: https://docs.cloud.google.com/billing/docs/how-to/budgets
- [C12] GCP spend caps: https://docs.cloud.google.com/billing/docs/how-to/budgets-spend-caps
- [C13] Cloud Run GPU: https://docs.cloud.google.com/run/docs/configuring/services/gpu
- [C14] Cloud Run pricing: https://cloud.google.com/run/pricing
- [C15] Forum, trial GPU quota 0 (Nov 2025): https://discuss.google.dev/t/300-free-trial-is-useless-without-gpu-quota-my-trial-period-is-wasting-away/290091
- [C16] Forum, paid account Cloud Run quota 0 (Jan 2026): https://discuss.google.dev/t/cloud-run-gpu-quota-not-activated-after-50-hours-on-paid-account/323795
- [C17] Forum, `GPUS_ALL_REGIONS` 0: https://discuss.google.dev/t/quota-gpus-all-regions-exceeded-limit-0-0-globally/121596
- [C18] Google teaching credits:
  - https://cloud.google.com/edu/faculty
  - https://edu.google.com/intl/ALL_us/programs/credits/teaching/
  - https://support.google.com/google-cloud-higher-ed/answer/10324552
- [C20] Azure for Students: https://azure.microsoft.com/en-us/free/students
- [C21] MS Q&A, GPU after upgrade: https://learn.microsoft.com/en-us/answers/questions/2157302/clarification-on-gpu-access-after-upgrading-azure
- [C22] MS Tech Community, student restrictions (Jun 2026): https://techcommunity.microsoft.com/discussions/microsoft-learn-for-educators/sku-quota-and-policy-restrictions-on-azure-for-students-and-free-subscriptions/4525160
- [C23] MS Q&A, v1–v4 capacity restrictions (Sep 2026): https://learn.microsoft.com/en-sg/answers/questions/5991236/azure-for-students-vm-deployment-blocked-by-subscr
- [C30] AWS Free Tier: https://aws.amazon.com/free/
- [C31] AWS free vs paid plan: https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/free-tier-plans.html
- [C32] AWS free plan hands-on (instance restrictions, card): https://dev.classmethod.jp/en/articles/try-new-aws-free-tier-2025/
- [C40] GitHub Education, DigitalOcean leaving the pack: https://github.com/orgs/community/discussions/201240
  - Secondary (GPU exclusion from 8 Jun 2026): https://aistudentdiscount.com/digitalocean-github-student-developer-pack-credits/
- [C41] GitHub Student Pack: https://education.github.com/pack
- [C50] Oracle Always Free resources: https://docs.oracle.com/en-us/iaas/Content/FreeTier/freetier_topic-Always_Free_Resources.htm
- [C51] Oracle Free Tier FAQ: https://www.oracle.com/cloud/free/faq/
- [C52] Oracle service limits: https://docs.oracle.com/en-us/iaas/Content/General/Concepts/servicelimits.htm
- [C60] OVHcloud free trial:
  - https://www.ovhcloud.com/en-ie/public-cloud/free-trial/
  - https://www.ovhcloud.com/it/public-cloud/free-trial/
  - Voucher-missing report (Sep 2026): https://community.ovhcloud.com/t/public-cloud-free-trial-200-not-showing-in-credits-vouchers/53908
- [C61] OVHcloud prices, incl. AI Deploy table: https://www.ovhcloud.com/en-ie/public-cloud/prices/
- [C62] AI Deploy capabilities and limits: https://docs.ovhcloud.com/en/guides/public-cloud/ai-machine-learning/ai-deploy-capabilities
- [C63] AI Deploy billing: https://docs.ovhcloud.com/en/guides/public-cloud/ai-machine-learning/ai-deploy-billing
- [C64] AI Deploy getting started: https://docs.ovhcloud.com/en/guides/public-cloud/ai-machine-learning/ai-deploy-getting-started
- [C65] AI Deploy custom image (UID 42420): https://docs.ovhcloud.com/en/guides/public-cloud/ai-machine-learning/ai-deploy-build-use-custom-image
- [C66] OVH quotas: https://docs.ovhcloud.com/en/guides/public-cloud/cross-functional/increasing-public-cloud-quota
- [C67] OVH GPU instances: https://docs.ovhcloud.com/en/guides/public-cloud/compute/deploy-a-gpu-instance
- [C68] Anecdote, OVH ID verification (2023): https://lowendspirit.com/discussion/5978/ovh-does-account-verification-dont-use-when-in-a-hurry
- [C70] Colab FAQ: https://research.google.com/colaboratory/faq.html
- [C71] Kaggle limits (secondary, Ultralytics docs): https://docs.ultralytics.com/integrations/kaggle
- [C72] HF ZeroGPU: https://huggingface.co/docs/hub/spaces-zerogpu
- [C73] Lightning AI free tier (secondary): https://aimultiple.com/free-cloud-gpu
- [C74] RunPod academic: https://www.runpod.io/academic-research
- [C75] Scaleway GPU prices: https://www.scaleway.com/en/pricing/gpu/
- [C76] Hetzner GEX:
  - https://www.hetzner.com/dedicated-rootserver/matrix-gpu/
  - Price (secondary): https://dohohub.com/news/hetzner-gex45-entry-level-gpu-server
- [C77] NVIDIA Academic Grant: https://www.nvidia.com/en-us/industries/higher-education-research/academic-grant-program/
- [C80] UniTrento HPC:
  - https://sites.google.com/unitn.it/hpc/home (redirects to KB S00111)
  - Usage example: https://github.com/civts/parallel-closest-pair
- [C81] vLLM GPU requirements: https://docs.vllm.ai/en/latest/getting_started/installation/gpu.html
