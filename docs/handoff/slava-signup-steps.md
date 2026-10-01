# Sign-up steps for Slava

**Every provider claim below was re-checked on 2026-10-01** at the source listed in [Sources](#sources) (`[Sx]`). **UNVERIFIED** = not confirmed on a primary page today.
Binding decisions: `DECISIONS.md` D1 (Modal primary, $28 budget / $0 spend limit, GCP backup) and D2 (gateway + SQLite on a separate always-on CPU VM, deployed by Jan).
Only you enter card or identity data. Nobody else signs up on your behalf.

## Overview

| What | Provider | Cost guard | Status |
|---|---|---|---|
| GPU (primary) | Modal, L40S, vLLM, Qwen3.6-35B-A3B-FP8 | Workspace budget $28 + spend limit $0 | sign up today |
| Gateway VM | Oracle Cloud Always Free (A1 Arm) | Always Free accounts can't be charged unless upgraded [S6] | sign up today |
| Gateway VM, if Oracle fails | Azure for Students (no card) | Subscription is disabled when the $100 credit runs out [S10, S11] | only if needed |
| GPU backup (L2 standby) | GCP Cloud Run GPU | Cloud Run spend cap (preview), not instant [S14] | this week, optional |
| Course credits / UniTrento GPU | Professor | — | email today (`email-professor.md`) |

---

## TODAY

### Modal (primary GPU)

- [ ] **Sign up** at modal.com.
  - The Starter plan gives "$30 / month free compute", "3 workspace seats included" and "100 containers + 10 GPU concurrency" [S1].
- [ ] **Add a payment method.** It is required twice:
  - "Using a GPU requires having a valid payment method on file" [S2].
  - "Inviting members requires a verified account. If you haven't already, add a payment method to verify your account" [S3].
- [ ] **Usage & Billing → set both caps, before any GPU run** [S4]:
  - **Workspace budget = $28.** It is "a monthly cap for total Workspace usage", applied **before** credits.
  - **Spend limit = $0.** It is "a monthly cap on net charges (what you pay out of pocket after credits are applied)". When reached, Modal halts workloads that would cost you money.
  - **Why both must be set explicitly:** without a custom value, the default spend limit is "the cycle's usage limit minus credits", which can be > $0.
  - The page says nothing about enforcement lag, hence the $2 margin (critic B2).
  - Take a screenshot of both values for the ledger.
- [ ] **Invite Jan** (Settings → Members). Starter includes 3 seats [S1], so you, Jan, plus one spare.
  - Role **Member** is enough: a Member "can otherwise perform any action like running and deploying Apps and modifying Secrets", but cannot assign roles [S3].
  - Jan runs under your caps: budgets are workspace-level [S4]. That members draw on the same credits is my inference; the page doesn't say it.
  - Jan is the second person with console access (runbook D7). Don't invite anyone else.
- [ ] **Apply for academic credits:** modal.com/academics → "Apply Here" [S5].
  - Offer: "Graduate students, labs, and researchers can get up to $10k free compute credits" [S1].
  - Eligibility details and review time are not stated [S5]. **Plan as if it never arrives** (critic m10).
- [ ] **Start the ledger** (any spreadsheet):
  - Columns: date, who, GPU, minutes, $ (from the dashboard), remaining.
  - Rule: nothing runs if it would leave **< $12** for the lab (D1).
  - Rough cost: L40S GPU is $0.000542/s ≈ $1.95/h [S1], ≈ $2.40/h with CPU/RAM (critic estimate). $28 ≈ 11 L40S-hours.
  - The $30 is **per month** [S1]. If the lab falls in November, October's spending doesn't reduce November's credit.

### Email the professor

- [ ] Send `email-professor.md`: lab date, course credits / UniTrento GPU, "local" definition, campus network.

### Oracle Cloud Always Free (gateway VM)

- [ ] **Read before signing up:**
  - **The home region cannot be changed.** "Oracle assigns your home region and you can't change it" [S9].
  - Always Free resources exist **only in the home region**: "Volumes created outside of the home region incur regular block volume costs" [S7].
  - "Free Tier is generally available in regions where commercial OCI service is available", and "Availability to Free Tier is subject to capacity limits" [S8].
- [ ] **Pick the home region.** Oracle's region table [S12]:

  | Region | Availability domains (ADs) |
  |---|---|
  | `eu-milan-1` (Milan) | 1 |
  | `eu-turin-1` (Turin) | 1 |
  | `eu-frankfurt-1` (Frankfurt) | 3 |

  - For an "out of host capacity" error, Oracle suggests three things: try a different AD, wait and retry, or upgrade to Pay As You Go [S7].
  - **In Milan, only "wait and retry" is possible.**
  - **Recommendation: Frankfurt** for better A1 odds. The extra latency vs Milan is a few milliseconds (UNVERIFIED), negligible next to model latency.
  - Choose Milan only if you prefer Italy. Whether Milan is offered at sign-up is UNVERIFIED: "Exact regions available for Free Tier may differ during signup" (Oracle free page, checked 2026-09-30).
- [ ] **Sign up** at oracle.com/cloud/free.
  - **Card:** "credit cards and debit cards that function like credit cards". **Not** PIN debit, virtual, single-use or prepaid cards [S6].
  - **Card check:** Oracle "may periodically check the validity of your card, resulting in a temporary 'authorization' hold", removed "typically within three to five days", with no actual charge [S6].
  - One free account per person [S6].
  - Phone/SMS verification at sign-up: UNVERIFIED (not on today's pages).
  - Never upgrade to Pay As You Go: an Always Free account cannot be charged unless upgraded [S6]. Upgrading removes that guarantee and breaks the €0 rule, so it needs your explicit PRD amendment.
- [ ] **Create the gateway VM** (or hand over to Jan once the account works):
  - **Image:** Ubuntu or Oracle Linux. **Shape:** `VM.Standard.A1.Flex` (Arm).
    - Always Free = 1,500 OCPU-hours + 9,000 GB-hours per month, "equivalent to 2 OCPUs and 12 GB of memory" [S7].
    - 1 OCPU / 6 GB is plenty for the gateway and may fit capacity more easily (UNVERIFIED).
    - Jan builds the image for `linux/arm64` (`HANDOFF-jan.md` §2).
  - **Stay within the Always Free limits from the start.** The trial's $300 / 30 days runs in parallel. If more A1 is provisioned than Always Free allows when the trial ends, "all existing Ampere A1 instances are disabled and then deleted after 30 days" [S6].
  - **Boot volume:** the default 50 GB counts toward the 200 GB free block storage [S7].
  - **If you get "Out of host capacity":**
    1. Try another AD (Frankfurt only).
    2. Retry every few hours. Scripted retries with the OCI CLI are common community practice (UNVERIFIED whether Oracle minds; keep them infrequent).
    3. Oracle-native fallback: the AMD `VM.Standard.E2.1.Micro` (1/8 OCPU, 1 GB RAM, up to 50 Mbps internet, amd64) [S7]. Enough for the gateway's few KB/s of streams (UNVERIFIED under load).
    4. **Still nothing by Friday 2 Oct:** switch to Azure for Students (below).

---

## THIS WEEK (by Fri 2 Oct, critic M6 milestone)

### Oracle VM network (with Jan)

- [ ] **VCN security list (cloud firewall):** ingress **443/tcp** from `0.0.0.0/0`, plus **22/tcp** from your and Jan's IPs only. Remove any SSH-from-anywhere rule. **No port 80**: Caddy uses TLS-ALPN on 443 (`HANDOFF-jan.md` §4). Whether the default security list opens 22 to the world: UNVERIFIED, check it.
- [ ] **Host firewall:** "Instances created using platform images have a default set of firewall rules that allow only SSH access" [S13].
  - Open 443 in iptables too.
  - On Ubuntu, edit the iptables rules and **don't use UFW**: "Using UFW to edit rules might cause an instance not to boot" [S13].
  - Never block link-local `169.254.0.2` [S13].
- [ ] **DNS name → VM IP.** Then Jan deploys Caddy + gateway (`HANDOFF-jan.md` §3–4).
- [ ] **Idle-reclamation risk.** Oracle may reclaim an Always Free instance if over 7 days its CPU (p95), network and memory (A1 only) all stay below 20 % [S7]. An idle gateway meets all three.
  - Before the lab, check weekly that the VM still exists.
  - Keep the image + compose file ready to redeploy in 15 minutes.
  - Mint the class keys late (runbook: T-1d), because keys live only on the VM.
  - Separately, "accounts left idle for 30 days or more may be deemed abandoned" [S6].

### Fallback VM, only if Oracle fails: Azure for Students

- [ ] **Sign up** with your `@studenti.unitn.it` address.
  - "No credit card required"; "$100 credit to use on Azure services within 12 months"; "Available only to full-time university students" [S10].
  - **Hard cap by design:** "Once your credit runs out, Azure disables your services and subscription". Charges only start if you upgrade to pay-as-you-go [S11].
- [ ] **VM size:** `Standard_B2ats_v2` (AMD, amd64) or `B2pts_v2` (Arm).
  - The free offer lists "750 hours each of B1s, B2pts v2 (Arm-based), and B2ats v2 (AMD-based)" [S10].
  - The B-series v2 is **not** among the series with capacity restrictions since July 2026 [S15].
  - Community guidance: stay under 4 vCPUs, avoid old B1s, choose "No infrastructure redundancy required" [S16].
- [ ] **Region:** find the allowed regions under Portal → Policy → Assignments → "Allowed resource deployment regions" [S16]. Pick the closest EU one.
- [ ] **Free amounts** for public IP, disk and bandwidth: not stated on the students page (UNVERIFIED). Anything beyond the free amounts comes out of the $100 credit, never from a card.

**Not recommended: GCP e2-micro Always Free.**
- It exists, but only in `us-west1`, `us-central1` and `us-east1`, with "1 GB of outbound data transfer … per month" [S17].
- The **public IPv4 is billed at $0.005/h** after a free tier "limited to one hour per month per account" [S18]. That is ≈ $3.6/month, so not €0.
- Compute Engine has no spend cap [S14].

### GCP Cloud Run GPU: backup / L2 standby (optional, critic B2 / D1)

- [ ] **Start the $300 / 90-day trial** (card required). Trial accounts **cannot use GPUs or request quota increases** [S17].
- [ ] **Upgrade to a paid billing account.** Remaining credit stays usable "within the original 90 days" [S17].
- [ ] **Create one project used only for this.** Set a **spend-cap budget** scoped to that project and the **Cloud Run** service [S14]:
  - Spend caps are in **Preview** and cover only Gemini API, Agent Platform, **Cloud Run** and Cloud Run functions.
  - They use **gross** cost ("don't include savings and credits") and pause the service at 100 % until you lift the cap manually.
  - Alerts fire at 50 % and 80 %.
  - Warning: "spend cap enforcement isn't instantaneous and any cost overages are billed as normal".
  - So set the cap well below the remaining credit (e.g. **$100**), which keeps any overage inside the credit.
  - Whether Preview needs allowlisting: UNVERIFIED.
- [ ] **Never create Compute Engine resources in that billing account**: no spend cap covers them [S14].
- [ ] **Which GPU:** Cloud Run offers L4 (24 GB; `europe-west1`, `europe-west4`) and RTX PRO 6000 Blackwell (96 GB; `europe-west4` only) [S19].
  - Our lab models need > 24 GB (37.5 GB / 31.2 GB in FP8), so the standby must be **RTX PRO 6000**.
  - It needs at least 20 CPU and 80 GiB [S19]. Cost ≈ $1.31/h GPU + $1.30/h CPU + $0.58/h RAM ≈ **$3.2/h** (my arithmetic from [S20]; europe-west4 is Tier 1).
  - "Minimum instances are charged at the full rate even when idle" [S19]: keep `min-instances=0` except during the lab.
- [ ] **Quota.** First-time use "automatically grant[s] 3,000 milliGPU quota" for RTX PRO 6000 [S19]. Forum reports (Nov 2025, Jan 2026) say the auto-grant sometimes never fires (`cloud-options.md` C15, C16).
  - Try one deploy this week.
  - **If no quota after 48 h, drop GCP** and write "no funded standby: L2 skipped" in the runbook.

---

## BEFORE REHEARSAL (runbook T-4d)

- [ ] **Modal caps re-checked** in Usage & Billing: budget $28, spend limit $0. **Ledger ≥ $12 left** after the planned rehearsal.
- [ ] **Academic credits:** check the answer. If granted, note the new amount, but keep the $28 / $0 caps unless you decide otherwise.
- [ ] **Jan has working access:** Modal (Member), SSH to the VM, the admin token (two-person rule, D7).
- [ ] **Gateway VM:** still exists (Oracle reclamation), `https://HOST/readyz` answers, only 443 (and 22 from your IPs) open.
- [ ] **Day-1 GPU checks** in `HANDOFF-jan.md` §7 passed. The streams-past-150-s result is recorded, and it sets `LGAI_MAX_TOKENS_CAP` to 1536 or 2048.
- [ ] **GCP standby:** cold start measured (< 8 min) and the model id known, or "no L2" written in the runbook.
- [ ] **Lab date confirmed** by the professor, and the countdown dates in `runbook-lab-day.md` filled in.
- [ ] **Model switch ready:** Szymon's smoke review (`szymon-api-guide.md` Part C) is scheduled for rehearsal day. Jan knows the one-line model swap.

---

## Sources

All checked 2026-10-01.

- [S1] Modal pricing: https://modal.com/pricing
- [S2] Modal GPU guide: https://modal.com/docs/guide/gpu
- [S3] Modal workspaces: https://modal.com/docs/guide/workspaces
- [S4] Modal budgets: https://modal.com/docs/guide/budgets
- [S5] Modal academics: https://modal.com/academics
- [S6] Oracle Free Tier FAQ: https://www.oracle.com/cloud/free/faq/
- [S7] Oracle Always Free resources: https://docs.oracle.com/en-us/iaas/Content/FreeTier/freetier_topic-Always_Free_Resources.htm
- [S8] Oracle Free Tier page: https://www.oracle.com/cloud/free/
- [S9] Oracle managing regions (home region FAQ): https://docs.oracle.com/en-us/iaas/Content/Identity/regions/managingregions.htm
- [S10] Azure for Students: https://azure.microsoft.com/en-us/free/students
- [S11] Azure for Students subscription disabled: https://learn.microsoft.com/en-us/azure/cost-management-billing/manage/azurestudents-subscription-disabled
- [S12] Oracle regions and availability domains: https://docs.oracle.com/en-us/iaas/Content/General/Concepts/regions.htm
- [S13] Oracle platform images, firewall rules: https://docs.oracle.com/en-us/iaas/Content/Compute/References/images.htm
- [S14] GCP spend caps: https://docs.cloud.google.com/billing/docs/how-to/budgets-spend-caps
- [S15] Azure previous-gen VM capacity limitations: https://learn.microsoft.com/en-us/azure/virtual-machines/migration/sizes/previous-gen-series-capacity-limitations
- [S16] Microsoft Tech Community, Azure for Students restrictions (community, Jun 2026): https://techcommunity.microsoft.com/discussions/microsoft-learn-for-educators/sku-quota-and-policy-restrictions-on-azure-for-students-and-free-subscriptions/4525160
- [S17] GCP Free Program (trial limits, e2-micro): https://docs.cloud.google.com/free/docs/free-cloud-features
- [S18] GCP VPC network pricing (external IPs): https://cloud.google.com/vpc/network-pricing
- [S19] Cloud Run GPU: https://docs.cloud.google.com/run/docs/configuring/services/gpu
- [S20] Cloud Run pricing: https://cloud.google.com/run/pricing
