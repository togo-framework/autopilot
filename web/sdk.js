/* Autopilot SDK (drop-in).
 * Adds a floating "Autopilot" launcher to any page — like the Feedback button —
 * that slides open the Mission Control board as an overlay. One line:
 *   <script src="/autopilot/sdk.js" defer></script>
 * Options via data-attrs: data-board (board URL, default /autopilot),
 * data-api (API base, default /api/autopilot), data-badge="off" to hide the
 * open-issue count. Exposes window.Autopilot ({open, close, toggle}). */
(function () {
  var s = document.currentScript || {};
  var board = (s.getAttribute && s.getAttribute("data-board")) || "/autopilot";
  var api = (s.getAttribute && s.getAttribute("data-api")) || "/api/autopilot";
  var badge = !((s.getAttribute && s.getAttribute("data-badge")) === "off");

  var css = ""
    + ".apx-btn{position:fixed;right:18px;bottom:64px;z-index:2147482900;display:flex;align-items:center;gap:8px;background:#8B5CF6;color:#fff;border:none;border-radius:24px;padding:11px 15px;font:600 13px system-ui;box-shadow:0 6px 20px rgba(0,0,0,.3);cursor:pointer}"
    + ".apx-btn .apx-count{background:rgba(255,255,255,.25);border-radius:20px;padding:1px 8px;font-size:11px;min-width:18px;text-align:center}"
    + ".apx-overlay{position:fixed;inset:0;z-index:2147483600;background:rgba(0,0,0,.5);display:none;justify-content:flex-end;backdrop-filter:saturate(120%) blur(1px)}"
    + ".apx-overlay.on{display:flex}"
    + ".apx-panel{width:min(960px,100%);height:100%;background:#0b0e13;box-shadow:-12px 0 40px rgba(0,0,0,.5);transform:translateX(24px);transition:transform .18s ease;display:flex;flex-direction:column}"
    + ".apx-overlay.on .apx-panel{transform:none}"
    + ".apx-bar{display:flex;align-items:center;gap:10px;padding:10px 14px;background:#141a21;border-bottom:1px solid #1d2530;color:#e8eef2;font:600 13px system-ui}"
    + ".apx-bar .apx-grow{flex:1}.apx-bar a{color:#8B5CF6;text-decoration:none;font-weight:500;font-size:12px}"
    + ".apx-bar button{background:transparent;border:1px solid #2a3340;color:#e8eef2;border-radius:8px;padding:5px 10px;font:inherit;cursor:pointer}"
    + ".apx-frame{flex:1;border:none;width:100%;background:#0b0e13}";

  function el(t, a, h) { var e = document.createElement(t); for (var k in a) e.setAttribute(k, a[k]); if (h != null) e.innerHTML = h; return e; }

  function mount() {
    var style = el("style"); style.textContent = css; document.head.appendChild(style);
    var btn = el("button", { class: "apx-btn", title: "Open Autopilot" }, "🛰️ Autopilot" + (badge ? ' <span class="apx-count" style="display:none">0</span>' : ""));
    var overlay = el("div", { class: "apx-overlay" });
    var panel = el("div", { class: "apx-panel" });
    panel.appendChild(el("div", { class: "apx-bar" },
      '🛰️ <span>Autopilot</span><span class="apx-grow"></span>'
      + '<a href="' + board + '" target="_blank">Open full board ↗</a>'
      + '<button class="apx-close">✕</button>'));
    var frame = el("iframe", { class: "apx-frame", loading: "lazy" });
    panel.appendChild(frame);
    overlay.appendChild(panel);
    document.body.appendChild(btn); document.body.appendChild(overlay);

    var loaded = false;
    function open() { if (!loaded) { frame.src = board; loaded = true; } overlay.classList.add("on"); }
    function close() { overlay.classList.remove("on"); }
    function toggle() { overlay.classList.contains("on") ? close() : open(); }
    btn.onclick = toggle;
    overlay.onclick = function (e) { if (e.target === overlay) close(); };
    panel.querySelector(".apx-close").onclick = close;
    document.addEventListener("keydown", function (e) { if (e.key === "Escape") close(); });
    window.Autopilot = { open: open, close: close, toggle: toggle };

    // live open-issue badge (backlog + ready + in_progress + blocked)
    if (badge) {
      var countEl = btn.querySelector(".apx-count");
      var refresh = function () {
        fetch(api + "/issues", { credentials: "include" }).then(function (r) { return r.ok ? r.json() : []; }).then(function (list) {
          var open = (list || []).filter(function (i) { return i.status !== "done" && i.status !== "in_review"; }).length;
          if (open > 0) { countEl.textContent = open; countEl.style.display = ""; } else { countEl.style.display = "none"; }
        }).catch(function () {});
      };
      refresh(); setInterval(refresh, 30000); // fallback; WS drives live updates
      (function connectWS() {
        try {
          var proto = location.protocol === "https:" ? "wss:" : "ws:";
          var ws = new WebSocket(proto + "//" + location.host + api + "/ws");
          ws.onmessage = function () { refresh(); };
          ws.onclose = function () { setTimeout(connectWS, 3000); };
          ws.onerror = function () { try { ws.close(); } catch (e) {} };
        } catch (e) {}
      })();
    }
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", mount); else mount();
})();
