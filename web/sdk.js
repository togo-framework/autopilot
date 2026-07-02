/* Autopilot SDK — ONE drop-in widget: a single draggable floating button that
 * opens ONE sidebar with Issues + Activity + Feedback, plus live push toasts.
 *   <script src="/autopilot/sdk.js" defer></script>
 * Renders natively (no iframe) inside a Shadow DOM mounted on <body>, so it sits
 * on the top stacking layer, immune to the host app's z-index/CSS. Styled after
 * the @togo-framework/ui feedback widget. data-attrs: data-api (/api/autopilot),
 * data-project (FAB position key), data-user. Exposes window.Autopilot. */
(function () {
  var sc = document.currentScript || {};
  var A = function (k, d) { return (sc.getAttribute && sc.getAttribute(k)) || d; };
  var api = A("data-api", "/api/autopilot");
  var project = A("data-project", "autopilot");
  var user = A("data-user", "");
  var POSKEY = "autopilot:fab:" + project;

  var KINDS = { bug: "#e5484d", feature: "#2C7BE2", question: "#f5a623", discussion: "#8B5CF6", chore: "#7C8B98", feedback: "#8B5CF6" };
  var STATUSES = [["backlog", "Backlog"], ["ready", "Ready"], ["in_progress", "In progress"], ["blocked", "Blocked"], ["in_review", "In review"], ["done", "Done"]];
  var STONE = { blocked: "#e5484d", in_review: "#f5a623", done: "#22C55E", in_progress: "#2C7BE2", ready: "#8B5CF6", backlog: "#7C8B98" };

  var CSS = "\n"
    + ":host{all:initial}\n"
    + "*{box-sizing:border-box;font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif}\n"
    + ".fab{position:fixed;z-index:2147483000;display:flex;align-items:center;gap:8px;background:#8B5CF6;color:#fff;border:none;border-radius:24px;padding:11px 15px;font-weight:600;font-size:13px;box-shadow:0 6px 20px rgba(0,0,0,.35);cursor:grab;user-select:none;touch-action:none}\n"
    + ".fab:active{cursor:grabbing}.fab .c{background:rgba(255,255,255,.25);border-radius:20px;padding:1px 8px;font-size:11px;min-width:18px;text-align:center}\n"
    + ".fab .dot{position:absolute;top:-4px;right:-4px;min-width:18px;height:18px;padding:0 5px;background:#e5484d;color:#fff;border-radius:20px;font-size:11px;font-weight:700;display:none;align-items:center;justify-content:center;box-shadow:0 0 0 2px #0b0e13}\n"
    + ".ov{position:fixed;inset:0;z-index:2147483400;background:rgba(0,0,0,.5);display:none;justify-content:flex-end}\n.ov.on{display:flex}\n"
    + ".panel{width:min(420px,100%);height:100%;background:#0b0e13;color:#e8eef2;display:flex;flex-direction:column;box-shadow:-12px 0 40px rgba(0,0,0,.5);font-size:13px}\n"
    + ".hd{display:flex;align-items:center;gap:8px;padding:12px 14px;border-bottom:1px solid #1d2530;font-weight:700}\n.hd .g{flex:1}\n"
    + ".tabs{display:flex;gap:4px;padding:8px 10px;border-bottom:1px solid #1d2530}\n"
    + ".tab{flex:1;padding:7px;border-radius:8px;background:transparent;border:1px solid #1d2530;color:#8aa;font:inherit;font-weight:600;cursor:pointer}\n.tab.on{background:#8B5CF6;border-color:#8B5CF6;color:#fff}\n"
    + ".body{flex:1;overflow:auto;padding:12px}\n"
    + "button{font:inherit;cursor:pointer}\n"
    + ".btn{padding:7px 12px;border-radius:8px;border:1px solid #2a3340;background:transparent;color:#e8eef2}\n.btn.p{background:#8B5CF6;border-color:#8B5CF6;color:#fff}\n"
    + "input,textarea,select{width:100%;background:#0b0e13;color:#e8eef2;border:1px solid #2a3340;border-radius:8px;padding:8px;font:inherit;margin-top:4px}\n"
    + ".grp{font-size:11px;text-transform:uppercase;letter-spacing:.06em;color:#8aa;margin:12px 0 6px}\n"
    + ".card{background:#141a21;border:1px solid #1d2530;border-radius:10px;padding:9px 10px;margin-bottom:7px;cursor:pointer}\n.card:hover{border-color:#8B5CF6}\n"
    + ".card .t{font-weight:600;line-height:1.3}\n.pill{display:inline-block;font-size:10px;padding:1px 7px;border-radius:20px;margin-right:5px}\n"
    + ".sb{display:inline-block;font-size:10px;padding:2px 8px;border-radius:20px;border:1px solid}\n"
    + ".row{display:flex;gap:6px;flex-wrap:wrap;margin:10px 0}\n.lbl{font-size:12px;color:#8aa;margin-top:8px;display:block}\n"
    + ".kind{padding:6px 10px;border-radius:8px;border:1px solid #2a3340;background:transparent;color:#8aa;font-weight:600}\n.kind.on{color:#fff}\n"
    + ".act{border-left:2px solid #2a3340;padding:6px 0 6px 10px;margin-bottom:2px}\n.act .m{line-height:1.35}\n.act .ts{font-size:10px;color:#667}\n"
    + "pre{white-space:pre-wrap;word-break:break-word;background:#141a21;border:1px solid #1d2530;border-radius:8px;padding:8px;font-size:12px}\n"
    + ".cmt{border:1px solid #1d2530;border-radius:8px;padding:8px;margin:6px 0}\n.cmt .w{font-size:11px;color:#8aa;margin-bottom:3px}\n.cmt.agent{border-color:#1e3a5f}\n"
    + ".muted{color:#8aa}.back{color:#8B5CF6;background:none;border:none;padding:0;margin-bottom:8px}\n"
    + ".toasts{position:fixed;top:14px;right:14px;z-index:2147483646;display:flex;flex-direction:column;gap:8px;max-width:330px}\n"
    + ".toast{background:#141a21;color:#e8eef2;border:1px solid #1d2530;border-left:3px solid #2C7BE2;border-radius:10px;padding:10px 12px;box-shadow:0 8px 24px rgba(0,0,0,.4);cursor:pointer;display:flex;gap:9px;align-items:flex-start}\n"
    + ".toast.blocked{border-left-color:#e5484d}.toast.review{border-left-color:#f5a623}.toast.done{border-left-color:#22C55E}.toast.comment{border-left-color:#8B5CF6}\n.toast .x{opacity:.5}\n";

  function h(tag, attrs, html) { var e = document.createElement(tag); for (var k in (attrs || {})) e.setAttribute(k, attrs[k]); if (html != null) e.innerHTML = html; return e; }
  function esc(s) { return (s == null ? "" : String(s)).replace(/[&<>]/g, function (c) { return { "&": "&amp;", "<": "&lt;", ">": "&gt;" }[c]; }); }
  function j(method, path, body) {
    return fetch(api + path, { method: method, credentials: "include", headers: { "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined })
      .then(function (r) { if (!r.ok) throw new Error(r.status); return r.status === 204 ? {} : r.json(); });
  }

  var state = { issues: [], titles: {}, activity: [], unread: 0, tab: "issues", openId: null };

  function boot() {
    var host = h("div", { "data-autopilot-sdk": "" }); document.body.appendChild(host);
    var root = host.attachShadow({ mode: "open" });
    root.appendChild(h("style", {}, CSS));

    var fab = h("button", { class: "fab", title: "Autopilot (drag to move)" }, '🛰️ Autopilot <span class="c" style="display:none">0</span><span class="dot"></span>');
    var ov = h("div", { class: "ov" });
    var panel = h("div", { class: "panel" });
    panel.appendChild(h("div", { class: "hd" }, '🛰️ <span>Autopilot</span><span class="g"></span><a class="back" href="/autopilot" target="_blank" style="font-size:12px">Full board ↗</a> <button class="btn x" style="padding:4px 9px">✕</button>'));
    var tabs = h("div", { class: "tabs" },
      '<button class="tab on" data-t="issues">Issues</button><button class="tab" data-t="activity">Activity</button><button class="tab" data-t="feedback">Feedback</button>');
    panel.appendChild(tabs);
    var bodyEl = h("div", { class: "body" }); panel.appendChild(bodyEl);
    ov.appendChild(panel);
    var toasts = h("div", { class: "toasts" });
    root.appendChild(fab); root.appendChild(ov); root.appendChild(toasts);

    var countEl = fab.querySelector(".c"), dot = fab.querySelector(".dot");
    function setUnread(n) { state.unread = n; dot.textContent = n > 9 ? "9+" : n; dot.style.display = n > 0 ? "flex" : "none"; }

    // ---- draggable FAB (press-drag moves; small move = click) ----
    (function () {
      var pos; try { pos = JSON.parse(localStorage.getItem(POSKEY)); } catch (e) {}
      if (pos) { fab.style.left = pos.left + "px"; fab.style.top = pos.top + "px"; fab.style.right = "auto"; fab.style.bottom = "auto"; }
      else { fab.style.right = "18px"; fab.style.bottom = "18px"; }
      var down = null, moved = false;
      fab.addEventListener("pointerdown", function (e) { down = { x: e.clientX, y: e.clientY, l: fab.offsetLeft, t: fab.offsetTop }; moved = false; fab.setPointerCapture(e.pointerId); });
      fab.addEventListener("pointermove", function (e) {
        if (!down) return; var dx = e.clientX - down.x, dy = e.clientY - down.y;
        if (Math.abs(dx) + Math.abs(dy) > 5) moved = true;
        if (moved) {
          var l = Math.min(Math.max(0, down.l + dx), innerWidth - fab.offsetWidth), t = Math.min(Math.max(0, down.t + dy), innerHeight - fab.offsetHeight);
          fab.style.left = l + "px"; fab.style.top = t + "px"; fab.style.right = "auto"; fab.style.bottom = "auto";
        }
      });
      fab.addEventListener("pointerup", function (e) {
        if (down && moved) { try { localStorage.setItem(POSKEY, JSON.stringify({ left: fab.offsetLeft, top: fab.offsetTop })); } catch (e) {} }
        else if (down) toggle();
        down = null;
      });
    })();

    function open() { ov.classList.add("on"); setUnread(0); render(); }
    function close() { ov.classList.remove("on"); }
    function toggle() { ov.classList.contains("on") ? close() : open(); }
    panel.querySelector(".x").onclick = close;
    ov.onclick = function (e) { if (e.target === ov) close(); };
    tabs.querySelectorAll(".tab").forEach(function (b) { b.onclick = function () { state.tab = b.getAttribute("data-t"); state.openId = null; tabs.querySelectorAll(".tab").forEach(function (x) { x.classList.toggle("on", x === b); }); render(); }; });

    // ---- render ----
    function render() {
      if (!ov.classList.contains("on")) return;
      if (state.openId) return renderDetail(state.openId);
      if (state.tab === "issues") return renderIssues();
      if (state.tab === "activity") return renderActivity();
      if (state.tab === "feedback") return renderFeedback();
    }
    function renderIssues() {
      var html = '<button class="btn p" style="width:100%" id="new">+ New issue</button>';
      STATUSES.forEach(function (s) {
        var items = state.issues.filter(function (i) { return i.status === s[0]; });
        if (!items.length) return;
        html += '<div class="grp">' + s[1] + ' · ' + items.length + '</div>';
        items.forEach(function (i) {
          html += '<div class="card" data-id="' + i.id + '"><div class="t">' + esc(i.title) + '</div><div style="margin-top:5px">'
            + '<span class="pill" style="background:' + (KINDS[i.kind] || "#334") + '22;color:' + (KINDS[i.kind] || "#8aa") + '">' + esc(i.kind) + '</span>'
            + (i.branch ? '<span class="pill" style="background:#1d2530;color:#8aa">⎇ ' + esc(i.branch.split("/").pop()) + '</span>' : '') + '</div></div>';
        });
      });
      if (!state.issues.length) html += '<p class="muted" style="text-align:center;margin-top:30px">No issues yet. Create one to hand to the agents.</p>';
      bodyEl.innerHTML = html;
      var nb = bodyEl.querySelector("#new"); if (nb) nb.onclick = renderNew;
      bodyEl.querySelectorAll(".card").forEach(function (c) { c.onclick = function () { state.openId = c.getAttribute("data-id"); render(); }; });
    }
    function renderNew() {
      bodyEl.innerHTML = '<button class="back">← back</button>'
        + '<label class="lbl">Title</label><input id="t" placeholder="e.g. Add a /health endpoint"/>'
        + '<label class="lbl">Details</label><textarea id="b" rows="4" placeholder="What should the agent do?"></textarea>'
        + '<label class="lbl">Type</label><div class="row" id="kinds">' + ["feature", "bug", "chore"].map(function (k) { return '<button class="kind" data-k="' + k + '">' + k + '</button>'; }).join("") + '</div>'
        + '<div class="row"><label class="muted"><input type="checkbox" id="rdy" style="width:auto"/> hand to agents now</label></div>'
        + '<div class="row"><button class="btn p" id="create">Create</button></div>';
      var kind = "feature";
      bodyEl.querySelector(".back").onclick = function () { render(); };
      var setKind = function () { bodyEl.querySelectorAll(".kind").forEach(function (b) { var on = b.getAttribute("data-k") === kind; b.classList.toggle("on", on); b.style.background = on ? KINDS[kind] : "transparent"; b.style.borderColor = on ? KINDS[kind] : "#2a3340"; }); };
      bodyEl.querySelectorAll(".kind").forEach(function (b) { b.onclick = function () { kind = b.getAttribute("data-k"); setKind(); }; }); setKind();
      bodyEl.querySelector("#create").onclick = function () {
        var t = bodyEl.querySelector("#t").value.trim(); if (!t) return;
        j("POST", "/issues", { title: t, body: bodyEl.querySelector("#b").value, kind: kind, status: bodyEl.querySelector("#rdy").checked ? "ready" : "backlog", created_by: user || "human" })
          .then(function () { state.tab = "issues"; state.openId = null; refresh().then(render); });
      };
    }
    function renderDetail(id) {
      j("GET", "/issues/" + id).then(function (d) {
        var i = d.issue, cs = d.comments || [];
        var acts = [];
        if (i.status === "backlog") acts.push('<button class="btn p" data-s="ready">Hand to agents →</button>');
        if (i.status === "in_review") { acts.push('<button class="btn p" data-s="done">Accept ✓</button>'); acts.push('<button class="btn" data-s="ready">Re-run</button>'); }
        if (i.status === "blocked") acts.push('<span class="muted">Reply below to unblock ↓</span>');
        bodyEl.innerHTML = '<button class="back">← issues</button>'
          + '<div><span class="sb" style="color:' + STONE[i.status] + ';border-color:' + STONE[i.status] + '">' + i.status + '</span>' + (i.pr_url ? ' <a class="back" href="' + esc(i.pr_url) + '" target="_blank">PR ↗</a>' : '') + '</div>'
          + '<h3 style="margin:8px 0 2px">' + esc(i.title) + '</h3>'
          + (i.body ? '<pre>' + esc(i.body) + '</pre>' : '')
          + '<div class="row">' + acts.join(" ") + '</div>'
          + (i.result ? '<div class="grp">last agent result</div><pre>' + esc(i.result) + '</pre>' : '')
          + '<div class="grp">thread</div><div id="th">' + (cs.map(function (c) { return '<div class="cmt ' + c.author_kind + '"><div class="w"><b>' + esc(c.author) + '</b> · ' + c.author_kind + '</div>' + esc(c.body).replace(/\n/g, "<br>") + '</div>'; }).join("") || '<span class="muted">No comments.</span>') + '</div>'
          + '<textarea id="cb" rows="2" placeholder="' + (i.status === "blocked" ? "Answer to unblock…" : "Comment…") + '"></textarea><div class="row"><button class="btn p" id="send">Comment' + (i.status === "blocked" ? " & unblock" : "") + '</button></div>';
        bodyEl.querySelector(".back").onclick = function () { state.openId = null; render(); };
        bodyEl.querySelectorAll("[data-s]").forEach(function (b) { b.onclick = function () { j("POST", "/issues/" + id + "/status", { status: b.getAttribute("data-s") }).then(function () { refresh().then(render); }); }; });
        bodyEl.querySelector("#send").onclick = function () { var v = bodyEl.querySelector("#cb").value.trim(); if (!v) return; j("POST", "/issues/" + id + "/comments", { author: user || "human", author_kind: "human", body: v }).then(function () { renderDetail(id); refresh(); }); };
      });
    }
    function renderActivity() {
      bodyEl.innerHTML = state.activity.length
        ? state.activity.map(function (a) { return '<div class="act" style="border-left-color:' + (a.color || "#2a3340") + '"><div class="m">' + a.icon + ' ' + esc(a.msg) + '</div><div class="ts">' + esc(a.ts) + '</div></div>'; }).join("")
        : '<p class="muted" style="text-align:center;margin-top:30px">No activity yet. Events appear here live.</p>';
    }
    function renderFeedback() {
      bodyEl.innerHTML = '<p class="muted">Found a bug, have an idea, or a question about this page?</p>'
        + '<label class="lbl">Type</label><div class="row" id="fk">' + ["bug", "feature", "question", "discussion"].map(function (k) { return '<button class="kind" data-k="' + k + '">' + k + '</button>'; }).join("") + '</div>'
        + '<label class="lbl">Message</label><textarea id="fb" rows="4" placeholder="What could be better?"></textarea>'
        + '<div class="row"><button class="btn p" id="fsend">Send feedback</button></div><div id="fok" class="muted" style="display:none;color:#22C55E">Thanks — filed ✓</div>';
      var fk = "bug";
      var setk = function () { bodyEl.querySelectorAll("#fk .kind").forEach(function (b) { var on = b.getAttribute("data-k") === fk; b.classList.toggle("on", on); b.style.background = on ? KINDS[fk] : "transparent"; b.style.borderColor = on ? KINDS[fk] : "#2a3340"; }); };
      bodyEl.querySelectorAll("#fk .kind").forEach(function (b) { b.onclick = function () { fk = b.getAttribute("data-k"); setk(); }; }); setk();
      bodyEl.querySelector("#fsend").onclick = function () {
        var v = bodyEl.querySelector("#fb").value.trim(); if (!v) return;
        var kind = (fk === "bug" || fk === "feature") ? fk : "feedback";
        j("POST", "/issues", { title: (v.length > 72 ? v.slice(0, 72) + "…" : v), body: v + "\n\n— " + fk + " · " + location.pathname, kind: kind, status: "backlog", created_by: user || "feedback" })
          .then(function () { bodyEl.querySelector("#fb").value = ""; var ok = bodyEl.querySelector("#fok"); ok.style.display = "block"; setTimeout(function () { ok.style.display = "none"; }, 1600); refresh(); });
      };
    }

    // ---- data + live ----
    function refresh() {
      return j("GET", "/issues").then(function (list) {
        state.issues = list || [];
        state.issues.forEach(function (i) { state.titles[i.id] = i.title; });
        var openN = state.issues.filter(function (i) { return i.status !== "done" && i.status !== "in_review"; }).length;
        countEl.textContent = openN; countEl.style.display = openN > 0 ? "" : "none";
      }).catch(function () {});
    }
    function describe(ev) {
      var t = state.titles[ev.id || ev.issue_id] || ev.title || "an issue";
      if (ev.type === "issue.created") return { sev: "new", icon: ev.kind === "feedback" ? "📣" : ev.kind === "bug" ? "🐞" : "🆕", msg: (ev.kind === "feedback" ? "New feedback: " : "New issue: ") + t, color: "#2C7BE2", id: ev.id };
      if (ev.type === "comment.added") return { sev: "comment", icon: "💬", msg: (ev.author_kind === "agent" ? "Agent commented on " : "New comment on ") + t, color: "#8B5CF6", id: ev.issue_id };
      if (ev.type === "issue.status") { var m = { blocked: ["blocked", "🚧", "Blocked — needs input: ", "#e5484d"], in_review: ["review", "✅", "Ready for review: ", "#f5a623"], in_progress: ["progress", "🤖", "Agent started: ", "#2C7BE2"], done: ["done", "🎉", "Done: ", "#22C55E"], ready: ["new", "📥", "Queued: ", "#8B5CF6"] }[ev.status]; if (!m) m = ["new", "↪", "Moved to " + ev.status + ": ", "#8aa"]; return { sev: m[0], icon: m[1], msg: m[2] + t, color: m[3], id: ev.id }; }
      return null;
    }
    function toast(n) {
      var c = h("div", { class: "toast " + n.sev }, '<span>' + n.icon + '</span><div style="flex:1">' + esc(n.msg) + '</div><span class="x">✕</span>');
      c.onclick = function (e) { if (e.target.className === "x") { c.remove(); return; } if (n.id) { state.tab = "issues"; state.openId = n.id; } open(); c.remove(); };
      toasts.appendChild(c); while (toasts.children.length > 4) toasts.removeChild(toasts.firstChild);
      if (n.sev !== "blocked") setTimeout(function () { c.remove(); }, 7000);
    }
    function onEvent(ev) {
      var n = describe(ev); if (!n) return;
      state.activity.unshift({ icon: n.icon, msg: n.msg, color: n.color, ts: new Date().toLocaleTimeString() });
      if (state.activity.length > 60) state.activity.pop();
      toast(n);
      if (!ov.classList.contains("on")) setUnread(state.unread + 1);
      refresh().then(function () { if (state.tab === "activity" || state.tab === "issues") render(); });
    }
    (function connectWS() {
      try {
        var ws = new WebSocket((location.protocol === "https:" ? "wss:" : "ws:") + "//" + location.host + api + "/ws");
        ws.onmessage = function (e) { var ev; try { ev = JSON.parse(e.data); } catch (_) { return; } if (ev.type !== "hello") onEvent(ev); };
        ws.onclose = function () { setTimeout(connectWS, 3000); };
        ws.onerror = function () { try { ws.close(); } catch (_) {} };
      } catch (e) {}
    })();
    refresh(); setInterval(refresh, 30000);
    window.Autopilot = { open: open, close: close, toggle: toggle, openIssue: function (id) { state.tab = "issues"; state.openId = id; open(); } };
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", boot); else boot();
})();
