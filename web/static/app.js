(() => {
  const $ = (sel) => document.querySelector(sel);
  const $$ = (sel) => [...document.querySelectorAll(sel)];

  let selectedDuration = "4h";
  let mutePrefill = {};

  const toast = (msg) => {
    const el = $("#toast");
    el.textContent = msg;
    el.hidden = false;
    clearTimeout(toast._t);
    toast._t = setTimeout(() => { el.hidden = true; }, 2600);
  };

  const fmtTime = (v) => {
    if (!v) return "—";
    const d = new Date(v);
    if (Number.isNaN(d.getTime())) return "—";
    const y = d.getFullYear();
    if (y < 2000 || y > 2100) return "—";
    return d.toLocaleString("zh-CN", { hour12: false });
  };

  const toLocalInput = (d) => {
    const pad = (n) => String(n).padStart(2, "0");
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
  };

  const fromLocalInput = (v) => (v ? new Date(v).toISOString() : null);

  const sevClass = (s) => {
    const x = (s || "").toLowerCase();
    if (x === "critical" || x === "error") return "sev-critical";
    if (x === "warning" || x === "warn") return "sev-warning";
    return "sev-info";
  };

  const statusClass = (s) => {
    if (s === "active") return "st-active";
    if (s === "scheduled") return "st-scheduled";
    return "st-muted";
  };

  const statusLabel = { active: "生效中", scheduled: "待生效", expired: "已过期" };
  const actionLabel = {
    notified: "已通知",
    muted: "已屏蔽",
    suppressed: "冷却抑制",
    recovered: "已恢复",
  };
  const incidentStatusLabel = {
    notified: "已通知",
    muted: "已屏蔽",
    suppressed: "冷却抑制",
    firing: "告警中",
    resolved: "已恢复",
  };

  async function api(path, opts = {}) {
    const res = await fetch(path, {
      headers: { "Content-Type": "application/json", ...(opts.headers || {}) },
      ...opts,
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || res.statusText);
    return data;
  }

  function updatePeriodHint() {
    const hint = $("#periodHint");
    const custom = $("#customRange");
    if (selectedDuration === "custom") {
      custom.hidden = false;
      const start = $("#fStartsAt").value;
      const end = $("#fExpiresAt").value;
      if (start && end) {
        hint.textContent = `自定义时间段：${fmtTime(start)} → ${fmtTime(end)}`;
      } else {
        hint.textContent = "请选择开始与结束时间";
      }
      return;
    }
    custom.hidden = true;
    if (!selectedDuration) {
      hint.textContent = "永久屏蔽，直到手动解除";
      return;
    }
    const map = { "1h": "1 小时", "4h": "4 小时", "12h": "12 小时", "24h": "1 天", "168h": "7 天" };
    hint.textContent = `将从现在起屏蔽 ${map[selectedDuration] || selectedDuration}`;
  }

  function openMuteDialog(prefill = {}) {
    mutePrefill = prefill;
    $("#fAlertname").value = prefill.alertname || "";
    $("#fHostname").value = prefill.hostname || "";
    $("#fInstance").value = prefill.instance || "";
    $("#fReason").value = prefill.reason || "";
    selectedDuration = "4h";
    $$("#durationChips .chip").forEach((c) => {
      c.classList.toggle("active", c.dataset.duration === "4h");
    });
    const now = new Date();
    const later = new Date(now.getTime() + 4 * 3600 * 1000);
    $("#fStartsAt").value = toLocalInput(now);
    $("#fExpiresAt").value = toLocalInput(later);
    updatePeriodHint();
    $("#muteDialog").showModal();
  }

  function renderIncidents(incidents) {
    const list = $("#incidentList");
    const empty = $("#incidentEmpty");
    list.innerHTML = "";
    if (!incidents.length) {
      empty.hidden = false;
      return;
    }
    empty.hidden = true;
    for (const inc of incidents) {
      const card = document.createElement("div");
      card.className = "incident-card";

      const statusCls = inc.status === "notified" ? "st-active"
        : inc.status === "resolved" ? "st-muted"
        : inc.status === "muted" ? "st-muted"
        : inc.status === "suppressed" ? "st-scheduled"
        : "st-active";
      const statusTxt = incidentStatusLabel[inc.status] || inc.status;

      const targets = (inc.targets || []).join(", ");
      const possible = (inc.possible || []).map(p => `<li>${esc(p)}</li>`).join("");
      const cardHtml = `
        <div class="incident-head">
          <div class="incident-left">
            <span class="pill ${sevClass(inc.severity)}">${esc(inc.severity || "unknown")}</span>
            <span class="incident-title">${esc(inc.alertname || inc.title || "—")}</span>
          </div>
          <span class="pill ${statusCls}">${statusTxt}</span>
        </div>
        <div class="incident-meta">
          <span>影响 ${inc.count || 0} 台</span>
          ${inc.job ? `<span>job: ${esc(inc.job)}</span>` : ""}
          <span>${fmtTime(inc.fired_at)}</span>
          ${inc.type ? `<span class="mono muted-text">${esc(inc.type)}</span>` : ""}
        </div>
        ${targets ? `<div class="incident-targets"><strong>受影响目标:</strong> ${esc(targets)}</div>` : ""}
        ${inc.suggestion ? `<div class="incident-suggestion"><strong>处理建议:</strong> ${esc(inc.suggestion)}</div>` : ""}
        ${possible ? `<div class="incident-possible"><strong>可能原因:</strong><ul>${possible}</ul></div>` : ""}
        ${inc.mute_reason ? `<div class="incident-mute-reason"><strong>屏蔽原因:</strong> ${esc(inc.mute_reason)}</div>` : ""}
      `;
      card.innerHTML = cardHtml;
      list.appendChild(card);
    }
  }

  function renderAlerts(alerts) {
    const tbody = $("#alertRows");
    const empty = $("#alertEmpty");
    tbody.innerHTML = "";
    if (!alerts.length) {
      empty.hidden = false;
      return;
    }
    empty.hidden = true;
    for (const a of alerts) {
      const tr = document.createElement("tr");
      const host = a.hostname || a.instance || "—";
      tr.innerHTML = `
        <td><span class="pill ${sevClass(a.severity)}">${a.severity || "unknown"}</span></td>
        <td class="mono">${esc(a.alertname || "—")}</td>
        <td>
          <div>${esc(host)}</div>
          ${a.job ? `<div class="muted-text">${esc(a.job)}</div>` : ""}
        </td>
        <td class="desc">${esc(a.description || a.value || "—")}</td>
        <td class="mono muted-text">${fmtTime(a.starts_at)}</td>
        <td>${a.muted ? '<span class="pill st-muted">已屏蔽</span>' : '<span class="pill st-active">推送中</span>'}</td>
        <td>
          <button class="btn ghost sm mute-btn" type="button">屏蔽</button>
        </td>`;
      tr.querySelector(".mute-btn").addEventListener("click", () => {
        openMuteDialog({
          alertname: a.alertname,
          hostname: a.hostname,
          instance: a.instance,
          reason: "临时屏蔽",
        });
      });
      tbody.appendChild(tr);
    }
  }

  function renderMutes(mutes) {
    const tbody = $("#muteRows");
    const empty = $("#muteEmpty");
    tbody.innerHTML = "";
    if (!mutes.length) {
      empty.hidden = false;
      return;
    }
    empty.hidden = true;
    for (const m of mutes) {
      const cond = [
        m.alertname && `alertname=${m.alertname}`,
        m.hostname && `hostname=${m.hostname}`,
        m.instance && `instance=${m.instance}`,
      ].filter(Boolean).join(" · ") || "（宽匹配）";
      const windowText = !m.expires_at
        ? `${fmtTime(m.starts_at || m.created_at)} → 永久`
        : `${fmtTime(m.starts_at || m.created_at)} → ${fmtTime(m.expires_at)}`;
      const tr = document.createElement("tr");
      tr.innerHTML = `
        <td><span class="pill ${statusClass(m.status)}">${statusLabel[m.status] || m.status}</span></td>
        <td class="mono">${esc(cond)}</td>
        <td class="muted-text">${esc(windowText)}</td>
        <td>${esc(m.reason || "—")}</td>
        <td><button class="btn danger sm" type="button">解除</button></td>`;
      tr.querySelector("button").addEventListener("click", async () => {
        if (!confirm("确认解除该屏蔽规则？")) return;
        try {
          await api(`/api/v1/mutes/${encodeURIComponent(m.id)}`, { method: "DELETE" });
          toast("已解除屏蔽");
          refresh();
        } catch (e) {
          toast(e.message);
        }
      });
      tbody.appendChild(tr);
    }
  }

  function renderHistory(events) {
    const list = $("#historyList");
    const empty = $("#historyEmpty");
    list.innerHTML = "";
    if (!events.length) {
      empty.hidden = false;
      return;
    }
    empty.hidden = true;
    for (const ev of events) {
      const div = document.createElement("div");
      div.className = "event";
      const targets = (ev.targets || []).slice(0, 5).join(", ");
      div.innerHTML = `
        <div class="event-time">${fmtTime(ev.time)}</div>
        <div>
          <div class="event-title">${actionLabel[ev.action] || ev.action} · ${esc(ev.alertname || "—")}</div>
          <div class="event-meta">
            <span class="pill ${sevClass(ev.severity)}">${esc(ev.severity || "—")}</span>
            · 影响 ${ev.count || 0}
            ${targets ? ` · ${esc(targets)}` : ""}
            ${ev.detail ? ` · ${esc(ev.detail)}` : ""}
          </div>
        </div>`;
      list.appendChild(div);
    }
  }

  function esc(s) {
    return String(s ?? "")
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;");
  }

  function fillChannelStatus(id, ch) {
    const el = $(id);
    if (!ch) {
      el.textContent = "未配置";
      el.className = "channel-status off";
      return;
    }
    if (ch.active) {
      el.textContent = "运行中：已启用且 webhook 有效";
      el.className = "channel-status ok";
    } else if (ch.enabled) {
      el.textContent = "已勾选启用，但 webhook 为空或仍是占位符";
      el.className = "channel-status warn";
    } else {
      el.textContent = "未启用";
      el.className = "channel-status off";
    }
  }

  function fillSettings(n) {
    if (!n) return;
    $("#sCluster").value = n.cluster || "";
    $("#sCooldown").value = n.cooldown || "";
    $("#sMaxItems").value = n.max_items ?? 10;
    $("#sSendResolved").checked = !!n.send_resolved;
    $("#feishuEnabled").checked = !!n.channels?.feishu?.enabled;
    $("#feishuURL").value = n.channels?.feishu?.webhook_url || "";
    $("#dingtalkEnabled").checked = !!n.channels?.dingtalk?.enabled;
    $("#dingtalkURL").value = n.channels?.dingtalk?.webhook_url || "";
    $("#wechatEnabled").checked = !!n.channels?.wechat?.enabled;
    $("#wechatURL").value = n.channels?.wechat?.webhook_url || "";
    fillChannelStatus("#feishuStatus", n.channels?.feishu);
    fillChannelStatus("#dingtalkStatus", n.channels?.dingtalk);
    fillChannelStatus("#wechatStatus", n.channels?.wechat);
    const active = (n.active_notifiers || []).join(", ") || "无";
    $("#settingsHint").textContent = `生效通道：${active} · 保存到 ${n.config_path || "config 文件"}`;
  }

  async function refresh() {
    try {
      const [dash, mutes, history, settings, incidents] = await Promise.all([
        api("/api/v1/dashboard"),
        api("/api/v1/mutes"),
        api("/api/v1/alerts/history"),
        api("/api/v1/settings/notification"),
        api("/api/v1/incidents"),
      ]);
      $("#clusterName").textContent = dash.cluster || "Smart Alert Aggregator";
      $("#mActive").textContent = dash.active_count ?? 0;
      $("#mIncidents").textContent = dash.incident_count ?? 0;
      $("#mMutes").textContent = dash.mute_active ?? 0;
      $("#mNotified").textContent = dash.stats?.notified_total ?? 0;
      $("#mMuted").textContent = dash.stats?.muted_total ?? 0;
      $("#mSuppressed").textContent = dash.stats?.suppressed_total ?? 0;
      const channels = (dash.notifiers || []).join(", ") || "未启用通道";
      $("#mMeta").textContent = `${channels} · cooldown ${dash.cooldown || "—"}`;
      renderAlerts(dash.active_alerts || []);
      renderIncidents(incidents.incidents || []);
      renderMutes(mutes.mutes || []);
      renderHistory(history.events || []);
      fillSettings(settings);
    } catch (e) {
      toast("加载失败: " + e.message);
    }
  }

  // Tabs
  $$(".tab").forEach((tab) => {
    tab.addEventListener("click", () => {
      $$(".tab").forEach((t) => t.classList.toggle("active", t === tab));
      $$(".panel").forEach((p) => p.classList.toggle("active", p.id === `panel-${tab.dataset.tab}`));
    });
  });

  // Duration chips
  $$("#durationChips .chip").forEach((chip) => {
    chip.addEventListener("click", () => {
      selectedDuration = chip.dataset.duration;
      $$("#durationChips .chip").forEach((c) => c.classList.toggle("active", c === chip));
      if (selectedDuration === "custom") {
        const now = new Date();
        if (!$("#fStartsAt").value) $("#fStartsAt").value = toLocalInput(now);
        if (!$("#fExpiresAt").value) {
          $("#fExpiresAt").value = toLocalInput(new Date(now.getTime() + 4 * 3600 * 1000));
        }
      }
      updatePeriodHint();
    });
  });
  $("#fStartsAt").addEventListener("change", updatePeriodHint);
  $("#fExpiresAt").addEventListener("change", updatePeriodHint);

  $("#btnNewMute").addEventListener("click", () => openMuteDialog({ reason: "手动屏蔽" }));
  $("#btnRefresh").addEventListener("click", refresh);
  $("#btnCancelMute").addEventListener("click", () => $("#muteDialog").close());

  $("#channelForm").addEventListener("submit", async (e) => {
    e.preventDefault();
    const body = {
      cluster: $("#sCluster").value.trim(),
      cooldown: $("#sCooldown").value.trim(),
      send_resolved: $("#sSendResolved").checked,
      max_items: Number($("#sMaxItems").value) || 10,
      channels: {
        feishu: {
          enabled: $("#feishuEnabled").checked,
          webhook_url: $("#feishuURL").value.trim(),
        },
        dingtalk: {
          enabled: $("#dingtalkEnabled").checked,
          webhook_url: $("#dingtalkURL").value.trim(),
        },
        wechat: {
          enabled: $("#wechatEnabled").checked,
          webhook_url: $("#wechatURL").value.trim(),
        },
      },
    };
    try {
      const res = await api("/api/v1/settings/notification", {
        method: "PUT",
        body: JSON.stringify(body),
      });
      fillSettings(res.notification);
      toast(`已保存，生效通道：${(res.active_notifiers || []).join(", ") || "无"}`);
      refresh();
    } catch (err) {
      toast(err.message);
    }
  });

  $("#muteForm").addEventListener("submit", async (e) => {
    e.preventDefault();
    const body = {
      alertname: $("#fAlertname").value.trim(),
      hostname: $("#fHostname").value.trim(),
      instance: $("#fInstance").value.trim(),
      reason: $("#fReason").value.trim(),
    };
    if (selectedDuration === "custom") {
      const starts = fromLocalInput($("#fStartsAt").value);
      const ends = fromLocalInput($("#fExpiresAt").value);
      if (!starts || !ends) {
        toast("请填写完整的开始与结束时间");
        return;
      }
      if (new Date(ends) <= new Date(starts)) {
        toast("结束时间必须晚于开始时间");
        return;
      }
      body.starts_at = starts;
      body.expires_at = ends;
    } else if (selectedDuration) {
      body.duration = selectedDuration;
    }
    // permanent: omit duration & expires_at

    try {
      await api("/api/v1/mutes", { method: "POST", body: JSON.stringify(body) });
      $("#muteDialog").close();
      toast("屏蔽规则已创建");
      refresh();
    } catch (err) {
      toast(err.message);
    }
  });

  refresh();
  setInterval(refresh, 15000);
})();
