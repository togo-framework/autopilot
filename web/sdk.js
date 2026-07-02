/* Autopilot SDK (drop-in).
 * Floating launcher that slides the Mission Control board open as an overlay
 * (like the Feedback button) PLUS a built-in push-notification system: live
 * WebSocket events (new issue, status move, comment, blocked) surface as
 * clickable toasts and an unread pip, so you can follow up without watching the
 * board. One line:  <script src="/autopilot/sdk.js" defer></script>
 * data-attrs: data-board (default /autopilot), data-api (default /api/autopilot),
 * data-badge="off", data-toasts="off". Exposes window.Autopilot. */
(function () {
  var s = document.currentScript || {};
  var get = function (k, d) { return (s.getAttribute && s.getAttribute(k)) || d; };
  var board = get("data-board", "/autopilot");
  var api = get("data-api", "/api/autopilot");
  var showBadge = get("data-badge") !== "off";
  var showToasts = get("data-toasts") !== "off";

  var css = ""
    + ".apx-btn{position:fixed;right:18px;bottom:64px;z-index:2147482900;display:flex;align-items:center;gap:8px;background:#8B5CF6;color:#fff;border:none;border-radius:24px;padding:11px 15px;font:600 13px system-ui;box-shadow:0 6px 20px rgba(0,0,0,.3);cursor:pointer}"
    + ".apx-btn .apx-count{background:rgba(255,255,255,.25);border-radius:20px;padding:1px 8px;font-size:11px;min-width:18px;text-align:center}"
    + ".apx-dot{position:absolute;top:-4px;right:-4px;min-width:18px;height:18px;padding:0 5px;background:#e5484d;color:#fff;border-radius:20px;font:700 11px system-ui;display:none;align-items:center;justify-content:center;box-shadow:0 0 0 2px #0b0e13}"
    + ".apx-toasts{position:fixed;top:16px;right:16px;z-index:2147483646;display:flex;flex-direction:column;gap:8px;max-width:340px}"
    + ".apx-toast{background:#141a21;color:#e8eef2;border:1px solid #1d2530;border-left:3px solid #2C7BE2;border-radius:10px;padding:10px 12px;font:13px system-ui;box-shadow:0 8px 24px rgba(0,0,0,.4);cursor:pointer;display:flex;gap:9px;align-items:flex-start;animation:apxin .18s ease}"
    + "@keyframes apxin{from{opacity:0;transform:translateX(16px)}to{opacity:1;transform:none}}"
    + ".apx-toast .ic{font-size:16px;line-height:1.2}.apx-toast .bd{flex:1}.apx-toast .mt{opacity:.55;font-size:11px;margin-top:2px}"
    + ".apx-toast .x{opacity:.5;padding:0 2px}.apx-toast .x:hover{opacity:1}"
    + ".apx-toast.blocked{border-left-color:#e5484d}.apx-toast.review{border-left-color:#f5a623}.apx-toast.done{border-left-color:#22C55E}.apx-toast.comment{border-left-color:#8B5CF6}"
    + ".apx-overlay{position:fixed;inset:0;z-index:2147483600;background:rgba(0,0,0,.5);display:none;justify-content:flex-end}"
    + ".apx-overlay.on{display:flex}"
    + ".apx-panel{width:min(960px,100%);height:100%;background:#0b0e13;box-shadow:-12px 0 40px rgba(0,0,0,.5);display:flex;flex-direction:column}"
    + ".apx-bar{display:flex;align-items:center;gap:10px;padding:10px 14px;background:#141a21;border-bottom:1px solid #1d2530;color:#e8eef2;font:600 13px system-ui}"
    + ".apx-bar .apx-grow{flex:1}.apx-bar a{color:#8B5CF6;text-decoration:none;font-weight:500;font-size:12px}"
    + ".apx-bar button{background:transparent;border:1px solid #2a3340;color:#e8eef2;border-radius:8px;padding:5px 10px;font:inherit;cursor:pointer}"
    + ".apx-frame{flex:1;border:none;width:100%;background:#0b0e13}";

  function el(t, a, h) { var e = document.createElement(t); for (var k in a) e.setAttribute(k, a[k]); if (h != null) e.innerHTML = h; return e; }
  function esc(x) { return (x == null ? "" : String(x)).replace(/[&<>]/g, function (c) { return { "&": "&amp;", "<": "&lt;", ">": "&gt;" }[c]; }); }

  var titles = {};   // issue id -> title (for notification text)
  var unread = 0;

  // Compose a human notification from a WS event (null = don't notify).
  function describe(ev) {
    var id = ev.id || ev.issue_id;
    var t = titles[id] || ev.title || "an issue";
    switch (ev.type) {
      case "issue.created":
        return { id: id, sev: "new", ic: ev.kind === "feedback" ? "📣" : ev.kind === "bug" ? "🐞" : "🆕",
          msg: (ev.kind === "feedback" ? "New feedback: " : ev.kind === "bug" ? "New bug: " : "New issue: ") + t };
      case "comment.added":
        return { id: id, sev: "comment", ic: "💬", msg: (ev.author_kind === "agent" ? "Agent commented on " : "New comment on ") + t };
      case "issue.status":
        var m = {
          blocked: { sev: "blocked", ic: "🚧", msg: "Blocked — needs your input: " + t },
          in_review: { sev: "review", ic: "✅", msg: "Ready for review: " + t },
          in_progress: { sev: "progress", ic: "🤖", msg: "Agent started: " + t },
          done: { sev: "done", ic: "🎉", msg: "Done: " + t },
          ready: { sev: "new", ic: "📥", msg: "Queued for agents: " + t }
        }[ev.status];
        if (!m) m = { sev: "new", ic: "↪", msg: "Moved to " + ev.status + ": " + t };
        m.id = id; return m;
      default: return null; // issue.updated / hello: too low-signal to toast
    }
  }

  function mount() {
    var style = el("style"); style.textContent = css; document.head.appendChild(style);

    var btn = el("button", { class: "apx-btn", title: "Open Autopilot" },
      "🛰️ Autopilot" + (showBadge ? ' <span class="apx-count" style="display:none">0</span>' : "") + '<span class="apx-dot"></span>');
    var overlay = el("div", { class: "apx-overlay" });
    var panel = el("div", { class: "apx-panel" });
    panel.appendChild(el("div", { class: "apx-bar" },
      '🛰️ <span>Autopilot</span><span class="apx-grow"></span><a href="' + board + '" target="_blank">Open full board ↗</a><button class="apx-close">✕</button>'));
    var frame = el("iframe", { class: "apx-frame", loading: "lazy" });
    panel.appendChild(frame);
    overlay.appendChild(panel);
    var toasts = el("div", { class: "apx-toasts" });
    document.body.appendChild(btn); document.body.appendChild(overlay); document.body.appendChild(toasts);

    var dot = btn.querySelector(".apx-dot");
    var countEl = showBadge ? btn.querySelector(".apx-count") : null;
    function setUnread(n) { unread = n; if (n > 0) { dot.textContent = n > 9 ? "9+" : n; dot.style.display = "flex"; } else dot.style.display = "none"; }

    function openTo(id) { frame.src = board + (id ? "?issue=" + encodeURIComponent(id) : ""); overlay.classList.add("on"); setUnread(0); }
    function open() { if (!frame.src) frame.src = board; overlay.classList.add("on"); setUnread(0); }
    function close() { overlay.classList.remove("on"); }
    btn.onclick = function () { overlay.classList.contains("on") ? close() : open(); };
    overlay.onclick = function (e) { if (e.target === overlay) close(); };
    panel.querySelector(".apx-close").onclick = close;
    document.addEventListener("keydown", function (e) { if (e.key === "Escape") close(); });

    function toast(n) {
      if (!showToasts) return;
      var card = el("div", { class: "apx-toast " + n.sev },
        '<span class="ic">' + n.ic + '</span><div class="bd">' + esc(n.msg) + '<div class="mt">Autopilot · click to open</div></div><span class="x">✕</span>');
      card.onclick = function (e) { if (e.target.className === "x") { card.remove(); return; } openTo(n.id); card.remove(); };
      toasts.appendChild(card);
      while (toasts.children.length > 4) toasts.removeChild(toasts.firstChild);
      if (n.sev !== "blocked") setTimeout(function () { card.remove(); }, 7000); // blocked stays until acted on
    }

    function notify(ev) {
      var n = describe(ev); if (!n) return;
      toast(n);
      if (!overlay.classList.contains("on")) setUnread(unread + 1);
    }

    function refresh() {
      return fetch(api + "/issues", { credentials: "include" }).then(function (r) { return r.ok ? r.json() : []; }).then(function (list) {
        (list || []).forEach(function (i) { titles[i.id] = i.title; });
        if (countEl) {
          var openN = (list || []).filter(function (i) { return i.status !== "done" && i.status !== "in_review"; }).length;
          if (openN > 0) { countEl.textContent = openN; countEl.style.display = ""; } else countEl.style.display = "none";
        }
      }).catch(function () {});
    }

    // WebSocket: primary live channel → toasts + badge. Poll is a fallback.
    (function connectWS() {
      try {
        var proto = location.protocol === "https:" ? "wss:" : "ws:";
        var ws = new WebSocket(proto + "//" + location.host + api + "/ws");
        ws.onmessage = function (e) {
          var ev; try { ev = JSON.parse(e.data); } catch (_) { return; }
          if (ev.type === "hello") return;
          notify(ev);
          refresh();
        };
        ws.onclose = function () { setTimeout(connectWS, 3000); };
        ws.onerror = function () { try { ws.close(); } catch (_) {} };
      } catch (e) {}
    })();

    refresh(); setInterval(refresh, 30000);
    window.Autopilot = { open: open, openTo: openTo, close: close, toggle: btn.onclick, notify: notify };
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", mount); else mount();
})();
