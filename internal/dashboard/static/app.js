// SONNIE dashboard: polls /api/panels and renders each panel.
//
// Adding a feature: register a dashboard.Panel in Go (internal/bot/dashboard.go)
// and, optionally, a renderer below under the same ID. A panel without a
// renderer is shown as plain data, so it works before it looks nice.
//
// All text goes through textContent (never innerHTML), so nothing the bot
// reports (channel names, log lines, webhook names) can inject markup.
"use strict";

(function () {
  const REFRESH_MS = 5000;
  const TZ = "Asia/Bangkok";

  // --- DOM helpers ---------------------------------------------------------

  function el(tag, attrs, ...children) {
    const node = document.createElement(tag);
    if (attrs) {
      for (const [k, v] of Object.entries(attrs)) {
        if (v === undefined || v === null || v === false) continue;
        if (k === "class") node.className = v;
        else if (k === "text") node.textContent = v;
        else node.setAttribute(k, v);
      }
    }
    for (const c of children.flat()) {
      if (c === undefined || c === null || c === false) continue;
      node.append(c instanceof Node ? c : document.createTextNode(String(c)));
    }
    return node;
  }

  function kv(rows) {
    const dl = el("dl", { class: "kv" });
    for (const [k, v] of rows) {
      if (v === undefined || v === null || v === "") continue;
      dl.append(el("dt", { text: k }), el("dd", null, v));
    }
    return dl;
  }

  function badge(text, kind) {
    return el("span", { class: "badge" + (kind ? " " + kind : ""), text: text });
  }

  function muted(text) {
    return el("p", { class: "muted", text: text });
  }

  function name(ref) {
    if (!ref) return "-";
    return ref.name || ref.id || "-";
  }

  function channel(ref) {
    return ref ? "#" + name(ref) : "-";
  }

  // --- formatting ----------------------------------------------------------

  function fmtTime(iso) {
    if (!iso) return "-";
    const d = new Date(iso);
    if (isNaN(d)) return "-";
    return d.toLocaleString("th-TH", { timeZone: TZ, dateStyle: "medium", timeStyle: "short" });
  }

  function fmtClock(iso) {
    if (!iso) return "-";
    const d = new Date(iso);
    return d.toLocaleTimeString("th-TH", { timeZone: TZ, hour: "2-digit", minute: "2-digit", second: "2-digit" });
  }

  function fmtDuration(sec) {
    sec = Math.max(0, Math.floor(sec));
    if (sec < 60) return sec + " วินาที";
    const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60);
    const parts = [];
    if (d) parts.push(d + " วัน");
    if (h) parts.push(h + " ชม.");
    if (m && !d) parts.push(m + " นาที");
    return parts.join(" ");
  }

  function fmtRelative(iso) {
    if (!iso) return "";
    const diff = (new Date(iso) - Date.now()) / 1000;
    if (Math.abs(diff) < 45) return "ตอนนี้";
    const txt = fmtDuration(Math.abs(diff));
    return diff > 0 ? "อีก " + txt : txt + "ที่แล้ว";
  }

  function when(iso) {
    if (!iso) return "-";
    return fmtTime(iso) + " (" + fmtRelative(iso) + ")";
  }

  function fmtBytes(n) {
    if (n === undefined || n === null) return "-";
    const units = ["B", "KB", "MB", "GB"];
    let i = 0;
    while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
    return n.toFixed(i ? 1 : 0) + " " + units[i];
  }

  function fmtMs(ms) {
    if (ms === undefined || ms === null) return "-";
    return ms >= 1000 ? (ms / 1000).toFixed(2) + " วินาที" : Math.round(ms) + " ms";
  }

  // --- renderers -----------------------------------------------------------

  const renderers = {};

  renderers.bot = function (body, d) {
    body.replaceChildren(
      el("p", null, d.online ? badge("ออนไลน์", "ok") : badge("ออฟไลน์", "bad"),
        d.online && d.latency_ms ? el("span", { class: "muted", text: "  ping gateway " + d.latency_ms + " ms" }) : null),
      kv([
        ["บอท", d.user || "-"],
        ["เวอร์ชัน", el("code", { text: d.version || "dev" })],
        ["เริ่มทำงาน", d.started_at ? fmtTime(d.started_at) : "-"],
        ["ทำงานมาแล้ว", fmtDuration(d.uptime_seconds || 0)],
        ["Go", d.go_version],
        ["goroutines", String(d.goroutines)],
        ["หน่วยความจำ (heap)", fmtBytes(d.heap_alloc_bytes)],
        ["หน่วยความจำจาก OS", fmtBytes(d.sys_bytes)],
        ["GC", String(d.num_gc)],
      ]),
      el("h3", { text: "เซิร์ฟเวอร์ (" + (d.guilds || []).length + ")" }),
      (d.guilds && d.guilds.length)
        ? el("ul", { class: "list" }, d.guilds.map((g) =>
          el("li", null, name(g), g.members ? el("span", { class: "muted", text: " · " + g.members + " คน" }) : null)))
        : muted("ยังไม่อยู่ในเซิร์ฟเวอร์ใด")
    );
  };

  renderers.tts_sessions = function (body, d) {
    if (!d.enabled) {
      body.replaceChildren(muted("ฟีเจอร์อ่านแชทปิดอยู่ (TTS_ENABLED=false)"));
      return;
    }
    if (!d.sessions || !d.sessions.length) {
      body.replaceChildren(muted("ไม่มีเซสชันอ่านแชทตอนนี้"));
      return;
    }
    body.replaceChildren(el("ul", { class: "list" }, d.sessions.map((s) =>
      el("li", null,
        el("div", null, el("strong", { text: "🔊 " + name(s.voice_channel) }), el("span", { class: "muted", text: " · " + name(s.guild) })),
        kv([
          ["เริ่ม", when(s.started_at)],
          ["ห้องแชท", (s.text_channels || []).map((c) => el("span", { class: "chip", text: channel(c) }))],
          ["คิว", String(s.queue)],
          ["อ่านแล้ว", s.read + " ข้อความ"],
          ["ลบแล้ว", s.deleted + " ข้อความ"],
        ]),
        el("h3", { text: "ผู้เข้าร่วม (" + (s.participants || []).length + ")" }),
        el("div", { class: "table-wrap" }, el("table", null,
          el("thead", null, el("tr", null, el("th", { text: "ผู้ใช้" }), el("th", { text: "ห้อง" }), el("th", { text: "เสียง" }))),
          el("tbody", null, (s.participants || []).map((p) =>
            el("tr", null, el("td", { text: name(p.user) }), el("td", { text: channel(p.channel) }), el("td", { text: p.voice || "-" })))))))
    )));
  };

  renderers.tts_usage = function (body, d) {
    if (!d.enabled) {
      body.replaceChildren(muted("ยังไม่ได้เปิดมิเตอร์ Google TTS (ไม่มีคีย์ Google หรือปิดฟีเจอร์อ่านแชท)"));
      return;
    }
    const bar = el("span");
    bar.style.width = Math.min(100, d.percent || 0) + "%";
    bar.style.backgroundColor = d.color || "var(--accent)";
    const nodes = [
      el("div", null, el("span", { class: "big", text: d.used_text }),
        d.limit ? el("span", { class: "muted", text: " / " + d.limit_text }) : el("span", { class: "muted", text: " (ไม่จำกัด)" }),
        d.limit ? el("span", { text: "  " + d.percent + "%" }) : null),
    ];
    if (d.limit) nodes.push(el("div", { class: "bar", role: "img", "aria-label": d.percent + "%" }, bar));
    nodes.push(kv([
      ["เดือน", d.month_label || d.month],
      ["สถานะ", d.paused ? badge("หยุดอ่านแล้ว (ครบงบ)", "bad") : badge("ใช้งานได้", "ok")],
      ["เสียงที่สร้าง", d.unit === "thb_micro" ? d.audio_text : null],
      ["แจ้งเตือนที่ส่งแล้ว", (d.alerted && d.alerted.length) ? d.alerted.map((a) => el("span", { class: "chip", text: a + "%" })) : "ยังไม่มี"],
      ["รีเซ็ต", d.next_reset ? when(d.next_reset) : null],
    ]));
    nodes.push(el("h3", { text: "เวลาถึงเสียงแรก (ล่าสุด)" }));
    if (d.latencies && d.latencies.length) {
      nodes.push(el("div", { class: "table-wrap" }, el("table", null,
        el("thead", null, el("tr", null, el("th", { text: "เวลา" }), el("th", { text: "ทาง" }), el("th", { text: "เสียงแรก" }), el("th", { text: "ทั้งหมด" }))),
        el("tbody", null, d.latencies.map((l) =>
          el("tr", null, el("td", { text: fmtClock(l.at) }), el("td", { text: l.path }), el("td", { text: fmtMs(l.first_audio_ms) }), el("td", { text: fmtMs(l.total_ms) })))))));
    } else {
      nodes.push(muted("ยังไม่มีข้อมูลตั้งแต่บอทเริ่มทำงาน"));
    }
    body.replaceChildren(...nodes);
  };

  const EVENT_STATUS = { scheduled: ["รอเริ่ม", ""], active: ["กำลังจัด", "ok"], completed: ["จบแล้ว", ""], canceled: ["ยกเลิก", "bad"] };

  renderers.events = function (body, d) {
    const s = d.scheduler || {};
    const nodes = [kv([
      ["ตัวจัดเวลา", s.running ? badge("ทำงาน", "ok") : badge("หยุด", "bad")],
      ["เตือนก่อน", s.remind_before_minutes ? s.remind_before_minutes + " นาที ใน " + channel(s.reminder_channel) : "ปิด"],
      ["เริ่ม/จบอีเวนต์ของคนอื่น", s.auto_start ? "อัตโนมัติ" : "เฉพาะที่สร้างด้วยบอท"],
      ["รอบถัดไป", s.next_wake ? when(s.next_wake) : "ไม่มีงานค้าง"],
      ["แหล่งรูปปก", (d.covers || []).map((c, i) => el("span", { class: "chip", text: (i + 1) + ". " + c.name + (c.paused_until ? " (พักถึง " + fmtTime(c.paused_until) + ")" : "") }))],
    ])];
    nodes.push(el("h3", { text: "อีเวนต์ที่กำลังจะมา (" + (d.events || []).length + ")" }));
    if (!d.events || !d.events.length) {
      nodes.push(muted("ไม่มีอีเวนต์"));
    } else {
      nodes.push(el("ul", { class: "list" }, d.events.map((e) => {
        const st = EVENT_STATUS[e.status] || [e.status, ""];
        return el("li", null,
          el("div", null, el("a", { href: e.url, target: "_blank", rel: "noopener noreferrer", text: e.name }), " ", badge(st[0], st[1])),
          kv([
            ["เริ่ม", when(e.start)],
            ["จบ", e.end ? fmtTime(e.end) : null],
            ["ที่", e.where],
            ["เซิร์ฟเวอร์", name(e.guild)],
            ["สนใจ", e.interested + " คน"],
            ["เตือน", e.remind_at ? when(e.remind_at) : null],
            ["บอทเริ่ม/จบให้", e.managed ? "ใช่" : "ไม่"],
            ["ผิดพลาด", e.failures ? e.failures + " ครั้ง" + (e.gave_up ? " (เลิกลองแล้ว)" : ", ลองใหม่ " + fmtRelative(e.retry_at)) : null],
          ]));
      })));
    }
    body.replaceChildren(...nodes);
  };

  renderers.logs = function (body, d, section) {
    section.classList.add("wide");
    let input = body.querySelector("input[type=search]");
    let pre = body.querySelector("pre.log");
    let count = body.querySelector(".log-count");
    if (!input) {
      input = el("input", { type: "search", placeholder: "กรอง log…", "aria-label": "กรอง log" });
      count = el("span", { class: "muted small log-count" });
      pre = el("pre", { class: "log", tabindex: "0" });
      input.addEventListener("input", () => paintLog(pre, count, input.value, body._lines || []));
      body.replaceChildren(el("div", { class: "log-tools" }, input, count), pre);
    }
    body._lines = d.lines || [];
    paintLog(pre, count, input.value, body._lines);
  };

  function paintLog(pre, count, filter, lines) {
    const atBottom = pre.scrollHeight - pre.scrollTop - pre.clientHeight < 24;
    const f = filter.trim().toLowerCase();
    const shown = f ? lines.filter((l) => l.text.toLowerCase().includes(f)) : lines;
    pre.replaceChildren(...shown.map((l) => el("div", { text: l.text })));
    count.textContent = f ? shown.length + " / " + lines.length + " บรรทัด" : lines.length + " บรรทัด";
    if (atBottom) pre.scrollTop = pre.scrollHeight;
  }

  renderers.webhooks = function (body, d) {
    const nodes = [];
    if (!d.hooks || !d.hooks.length) {
      nodes.push(muted("ยังไม่ได้ตั้งค่า webhook (WEBHOOKS)"));
    } else {
      nodes.push(el("div", { class: "table-wrap" }, el("table", null,
        el("thead", null, el("tr", null, el("th", { text: "ชื่อ" }), el("th", { text: "ส่งไปที่" }), el("th", { text: "เรียก" }), el("th", { text: "ล่าสุด" }))),
        el("tbody", null, d.hooks.map((h) =>
          el("tr", null, el("td", null, el("code", { text: "/hooks/" + h.name })), el("td", { text: channel(h.channel) }), el("td", { text: String(h.calls) }),
            el("td", { text: h.last_call ? fmtTime(h.last_call) + " · " + h.last_status : "-" })))))));
      nodes.push(el("p", { class: "muted small", text: "จำกัด " + d.rate_limit_per_minute + " ครั้ง/นาที ต่อ hook · body ไม่เกิน " + fmtBytes(d.max_body_bytes) }));
    }
    nodes.push(el("h3", { text: "การเรียกล่าสุด" }));
    if (!d.recent || !d.recent.length) {
      nodes.push(muted("ยังไม่มีการเรียก"));
    } else {
      nodes.push(el("div", { class: "table-wrap" }, el("table", null,
        el("thead", null, el("tr", null, el("th", { text: "เวลา" }), el("th", { text: "hook" }), el("th", { text: "ผล" }), el("th", { text: "จาก" }))),
        el("tbody", null, d.recent.slice(0, 30).map((c) =>
          el("tr", null, el("td", { text: fmtTime(c.at) }), el("td", { text: c.hook + (c.known ? "" : " (?)") }),
            el("td", null, badge(String(c.status), c.status < 300 ? "ok" : c.status === 429 ? "warn" : "bad"), c.error ? el("span", { class: "muted small", text: " " + c.error }) : null),
            el("td", { text: c.source_ip })))))));
    }
    body.replaceChildren(...nodes);
  };

  function renderGeneric(body, d) {
    body.replaceChildren(el("pre", { class: "raw", text: JSON.stringify(d, null, 2) }));
  }

  // --- refresh loop --------------------------------------------------------

  const state = document.getElementById("refresh-state");
  const container = document.getElementById("panels");

  function sectionFor(p) {
    let section = document.getElementById("panel-" + p.id);
    if (!section) {
      section = el("section", { class: "card panel", id: "panel-" + p.id, "data-panel": p.id },
        el("h2", { text: p.title }), el("div", { class: "panel-body" }));
      container.append(section);
    }
    return section;
  }

  function render(panels) {
    for (const p of panels) {
      const section = sectionFor(p);
      const body = section.querySelector(".panel-body");
      if (p.error) {
        body.replaceChildren(el("p", { class: "error", text: "โหลดไม่สำเร็จ: " + p.error }));
        continue;
      }
      try {
        (renderers[p.id] || renderGeneric)(body, p.data || {}, section);
      } catch (err) {
        body.replaceChildren(el("p", { class: "error", text: "แสดงผลไม่สำเร็จ: " + err }));
      }
    }
  }

  let timer = null;

  async function refresh() {
    clearTimeout(timer);
    try {
      const res = await fetch("/api/panels", { credentials: "same-origin", cache: "no-store", headers: { Accept: "application/json" } });
      if (res.status === 401 || res.status === 403) {
        // Logged out or no longer allowed: the page explains.
        window.location.reload();
        return;
      }
      if (!res.ok) throw new Error("HTTP " + res.status);
      const data = await res.json();
      render(data.panels || []);
      state.textContent = "อัปเดตล่าสุด " + fmtClock(data.generated_at) + " · รีเฟรชทุก " + REFRESH_MS / 1000 + " วินาที";
      state.classList.remove("error");
    } catch (err) {
      state.textContent = "เชื่อมต่อไม่ได้ (" + err.message + ") · จะลองใหม่";
      state.classList.add("error");
    }
    if (!document.hidden) timer = setTimeout(refresh, REFRESH_MS);
  }

  document.addEventListener("visibilitychange", () => {
    if (!document.hidden) refresh();
    else clearTimeout(timer);
  });

  refresh();
})();
