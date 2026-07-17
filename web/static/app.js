(() => {
  const $ = (sel) => document.querySelector(sel);
  const $$ = (sel) => [...document.querySelectorAll(sel)];

  let selectedDuration = "4h";
  let mutePrefill = {};

  // History filter state
  let historySeverity = "all";
  let historyTime = "all";
  let historyCustomStart = "";
  let historyCustomEnd = "";
  let historyPush = "all";
  let historyRecover = "all";
  let historyPage = 1;
  let historyPageSize = 20;
  let filteredEvents = [];

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

  const severityLabel = {
    disaster: "紧急",
    critical: "严重",
    warning: "告警",
    warn: "告警",
    error: "严重",
    info: "通知",
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
    firing: "推送失败",
  };
  const incidentStatusLabel = {
    notified: "已通知",
    muted: "已屏蔽",
    suppressed: "冷却抑制",
    firing: "告警中",
    resolved: "已恢复",
  };

  const pushRecover = (action) => {
    switch (action) {
      case "notified": return ["已通知", "未修复", "push-notified", "recover-unfixed"];
      case "recovered": return ["已通知", "已修复", "push-notified", "recover-fixed"];
      case "muted": return ["已屏蔽", "未修复", "push-muted", "recover-unfixed"];
      case "suppressed": return ["冷却抑制", "未修复", "push-suppressed", "recover-unfixed"];
      case "firing": return ["推送失败", "未修复", "push-failed", "recover-unfixed"];
      default: return [actionLabel[action] || action, "未知", "push-muted", "recover-unfixed"];
    }
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
      tr.className = "alert-row";
      const host = a.hostname || a.instance || "—";
      const expandId = "exp-" + Math.random().toString(36).slice(2, 8);
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
          <button class="btn ghost sm mute-btn" type="button" title="屏蔽此告警">屏蔽</button>
          <button class="btn ghost sm expand-btn" type="button" title="展开详情">详情</button>
        </td>`;
      tr.querySelector(".mute-btn").addEventListener("click", (e) => {
        e.stopPropagation();
        openMuteDialog({
          alertname: a.alertname,
          hostname: a.hostname,
          instance: a.instance,
          reason: "临时屏蔽",
        });
      });

      const detailRow = document.createElement("tr");
      detailRow.className = "alert-detail";
      detailRow.hidden = true;
      let labelsHtml = "";
      if (a.labels) {
        labelsHtml = Object.entries(a.labels)
          .map(([k, v]) => `<span class="tag">${esc(k)}<em>=</em><b>${esc(v)}</b></span>`)
          .join(" ");
      }
      let annotHtml = "";
      if (a.annotations) {
        annotHtml = Object.entries(a.annotations)
          .map(([k, v]) => `<span class="tag">${esc(k)}<em>=</em><b>${esc(v)}</b></span>`)
          .join(" ");
      }
      detailRow.innerHTML = `<td colspan="7">
        <div class="detail-box">
          ${labelsHtml ? `<div class="detail-block"><span class="detail-label">Labels</span><div class="tag-list">${labelsHtml}</div></div>` : ""}
          ${annotHtml ? `<div class="detail-block"><span class="detail-label">Annotations</span><div class="tag-list">${annotHtml}</div></div>` : ""}
          <div class="detail-inline">
            ${a.generator_url ? `<span class="detail-item"><span class="detail-label">Generator</span><a href="${esc(a.generator_url)}" target="_blank" rel="noopener">${esc(a.generator_url)}</a></span>` : ""}
            ${a.fingerprint ? `<span class="detail-item"><span class="detail-label">Fingerprint</span><code>${esc(a.fingerprint)}</code></span>` : ""}
            ${a.value ? `<span class="detail-item"><span class="detail-label">Value</span><code>${esc(a.value)}</code></span>` : ""}
            ${a.muted && a.mute_id ? `<span class="detail-item"><span class="detail-label">Mute ID</span><code>${esc(a.mute_id)}</code></span>` : ""}
          </div>
        </div>
      </td>`;

      tr.after(detailRow);
      tr.querySelector(".expand-btn").addEventListener("click", (e) => {
        e.stopPropagation();
        detailRow.hidden = !detailRow.hidden;
        tr.classList.toggle("expanded", !detailRow.hidden);
      });
      tr.addEventListener("click", (e) => {
        if (e.target.closest("button")) return;
        detailRow.hidden = !detailRow.hidden;
        tr.classList.toggle("expanded", !detailRow.hidden);
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

  function applyHistoryFilters(events) {
    let filtered = events;

    if (historySeverity !== "all") {
      filtered = filtered.filter(ev => (ev.severity || "").toLowerCase() === historySeverity);
    }

    if (historyTime !== "all") {
      const cutoff = timeFilterCutoff(historyTime);
      if (cutoff) {
        filtered = filtered.filter(ev => new Date(ev.time) >= cutoff);
      }
    } else if (historyCustomStart || historyCustomEnd) {
      const start = historyCustomStart ? new Date(historyCustomStart) : null;
      const end = historyCustomEnd ? new Date(historyCustomEnd) : null;
      if (start) start.setHours(0, 0, 0, 0);
      if (end) end.setHours(23, 59, 59, 999);
      filtered = filtered.filter(ev => {
        const d = new Date(ev.time);
        if (start && d < start) return false;
        if (end && d > end) return false;
        return true;
      });
    }

    if (historyPush !== "all") {
      filtered = filtered.filter(ev => {
        if (historyPush === "notified") return ev.action === "notified" || ev.action === "recovered";
        if (historyPush === "firing") return ev.action === "firing";
        if (historyPush === "muted") return ev.action === "muted";
        if (historyPush === "suppressed") return ev.action === "suppressed";
        return true;
      });
    }

    if (historyRecover !== "all") {
      filtered = filtered.filter(ev => {
        if (historyRecover === "resolved") return ev.action === "recovered";
        if (historyRecover === "unresolved") return ev.action !== "recovered";
        return true;
      });
    }

    return filtered;
  }

  function timeFilterCutoff(preset) {
    const now = new Date();
    switch (preset) {
      case "today":
        now.setHours(0, 0, 0, 0);
        return now;
      case "3d":
        now.setDate(now.getDate() - 3);
        return now;
      case "7d":
        now.setDate(now.getDate() - 7);
        return now;
      case "30d":
        now.setDate(now.getDate() - 30);
        return now;
      default:
        return null;
    }
  }

  function renderHistory(events) {
    const list = $("#historyList");
    const empty = $("#historyEmpty");
    const countEl = $("#historyCount");
    list.innerHTML = "";

    filteredEvents = applyHistoryFilters(events);
    if (countEl) countEl.textContent = `共 ${filteredEvents.length} 条`;

    if (!events.length) {
      empty.hidden = false;
      $("#historyPagination").hidden = true;
      return;
    }
    empty.hidden = true;

    if (!filteredEvents.length) {
      list.innerHTML = `<div class="empty">没有匹配的记录</div>`;
      $("#historyPagination").hidden = true;
      return;
    }

    const totalPages = Math.max(1, Math.ceil(filteredEvents.length / historyPageSize));
    if (historyPage > totalPages) historyPage = totalPages;
    const start = (historyPage - 1) * historyPageSize;
    const pageItems = filteredEvents.slice(start, start + historyPageSize);

    for (const ev of pageItems) {
      const div = document.createElement("div");
      div.className = "event";
      if (ev.action === "suppressed") div.classList.add("suppressed");
      const targets = (ev.targets || []).slice(0, 5).join(", ");
      const [pushTxt, recvTxt, pushCls, recvCls] = pushRecover(ev.action);
      const sv = ev.severity || "";
      div.innerHTML = `
        <span class="event-time">${fmtTime(ev.time)}</span>
        <div class="event-body">
          <div class="event-main">
            <div class="event-title">${esc(ev.alertname || "—")}</div>
            <div class="event-meta">
              <span class="pill ${sevClass(sv)}">${severityLabel[sv.toLowerCase()] || sv}</span>
              · 影响 ${ev.count || 0}
              ${targets ? ` · ${esc(targets)}` : ""}
              ${ev.detail ? ` · ${esc(ev.detail)}` : ""}
            </div>
          </div>
          <div class="event-pills">
            <span class="history-pill ${pushCls}">${pushTxt}</span>
            <span class="history-pill ${recvCls}">${recvTxt}</span>
          </div>
        </div>`;
      list.appendChild(div);
    }

    renderPagination(totalPages);
  }

  function renderPagination(totalPages) {
    const nav = $("#pageNav");
    const pagination = $("#historyPagination");
    if (!nav || totalPages <= 1) {
      if (pagination) pagination.hidden = true;
      return;
    }
    pagination.hidden = false;
    nav.innerHTML = "";

    const prev = document.createElement("button");
    prev.className = "page-btn";
    prev.textContent = "上一页";
    prev.disabled = historyPage <= 1;
    prev.addEventListener("click", () => { if (historyPage > 1) { historyPage--; refresh(); } });
    nav.appendChild(prev);

    for (let i = 1; i <= totalPages; i++) {
      const btn = document.createElement("button");
      btn.className = "page-btn" + (i === historyPage ? " active" : "");
      btn.textContent = i;
      btn.addEventListener("click", () => { historyPage = i; refresh(); });
      nav.appendChild(btn);
    }

    const next = document.createElement("button");
    next.className = "page-btn";
    next.textContent = "下一页";
    next.disabled = historyPage >= totalPages;
    next.addEventListener("click", () => { if (historyPage < totalPages) { historyPage++; refresh(); } });
    nav.appendChild(next);
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

    try {
      await api("/api/v1/mutes", { method: "POST", body: JSON.stringify(body) });
      $("#muteDialog").close();
      toast("屏蔽规则已创建");
      refresh();
    } catch (err) {
      toast(err.message);
    }
  });

  // History filter event handlers
  $("#historySeverity").addEventListener("change", (e) => {
    historySeverity = e.target.value;
    historyPage = 1;
    refresh();
  });

  $("#historyPush").addEventListener("change", (e) => {
    historyPush = e.target.value;
    historyPage = 1;
    refresh();
  });

  $("#historyRecover").addEventListener("change", (e) => {
    historyRecover = e.target.value;
    historyPage = 1;
    refresh();
  });

  $("#pageSizeSelect").addEventListener("change", (e) => {
    historyPageSize = parseInt(e.target.value, 10) || 20;
    historyPage = 1;
    refresh();
  });

  // Help dialog
  const helpFab = $("#helpFab");
  const helpDialog = $("#helpDialog");
  if (helpFab && helpDialog) {
    helpFab.addEventListener("click", () => helpDialog.showModal());
    $("#helpClose").addEventListener("click", () => helpDialog.close());
    helpDialog.addEventListener("click", (e) => {
      if (e.target === helpDialog) helpDialog.close();
    });
  }

  // Date popover
  const datePopoverBtn = $("#datePopoverBtn");
  const datePopoverPanel = $("#datePopoverPanel");

  // Toggle popover
  datePopoverBtn.addEventListener("click", (e) => {
    e.stopPropagation();
    datePopoverPanel.classList.toggle("open");
    datePopoverBtn.setAttribute("aria-expanded", datePopoverPanel.classList.contains("open"));
  });

  // Close popover when clicking outside
  document.addEventListener("click", (e) => {
    if (!datePopoverPanel.contains(e.target) && e.target !== datePopoverBtn) {
      datePopoverPanel.classList.remove("open");
      datePopoverBtn.setAttribute("aria-expanded", "false");
    }
  });

  // Tab switching inside popover
  $$("#datePopoverPanel .date-popover-tab").forEach((tab) => {
    tab.addEventListener("click", () => {
      $$("#datePopoverPanel .date-popover-tab").forEach(t => t.classList.toggle("active", t === tab));
      const isCustom = tab.dataset.tab === "custom";
      $("#datePopoverPanel .date-preset-chips").hidden = isCustom;
      $("#datePopoverPanel .date-custom-range").hidden = !isCustom;
    });
  });

  // Preset chips
  $$("#datePopoverPanel .date-preset-chip").forEach((chip) => {
    chip.addEventListener("click", () => {
      $$("#datePopoverPanel .date-preset-chip").forEach(c => c.classList.toggle("active", c === chip));
      historyTime = chip.dataset.time;
      historyCustomStart = "";
      historyCustomEnd = "";
      datePopoverBtn.textContent = chip.textContent;
      datePopoverPanel.classList.remove("open");
      datePopoverBtn.setAttribute("aria-expanded", "false");
      refresh();
    });
  });

  // Apply custom date range
  $("#datePopoverPanel .date-apply").addEventListener("click", () => {
    const start = $("#dateStart").value;
    const end = $("#dateEnd").value;
    if (!start || !end) {
      toast("请选择开始和结束日期");
      return;
    }
    if (new Date(start) > new Date(end)) {
      toast("开始日期不能晚于结束日期");
      return;
    }
    historyTime = "custom";
    historyCustomStart = start;
    historyCustomEnd = end;
    datePopoverBtn.textContent = `${start} ~ ${end}`;
    datePopoverPanel.classList.remove("open");
    datePopoverBtn.setAttribute("aria-expanded", "false");
    refresh();
  });

  refresh();
  setInterval(refresh, 15000);
})();