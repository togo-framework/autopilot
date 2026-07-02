/* Autopilot — Feedback SDK (drop-in).
 * Add a "feedback button everywhere" with one line:
 *   <script src="/autopilot/feedback.js" data-user="me@co"></script>
 * It injects a floating button; submissions POST to /api/autopilot/feedback
 * and land as `feedback` issues on the board. Also exposes window.AutopilotFeedback
 * ({open, submit}) so a framework component can drive it. */
(function () {
  var api = (document.currentScript && document.currentScript.getAttribute("data-api")) || "/api/autopilot";
  var user = (document.currentScript && document.currentScript.getAttribute("data-user")) || "";
  var css = ""
    + ".alf-btn{position:fixed;right:18px;bottom:18px;z-index:2147483000;background:#2C7BE2;color:#fff;border:none;border-radius:24px;padding:11px 16px;font:600 13px system-ui;box-shadow:0 6px 20px rgba(0,0,0,.3);cursor:pointer}"
    + ".alf-pop{position:fixed;right:18px;bottom:70px;z-index:2147483000;width:320px;background:#141a21;color:#e8eef2;border:1px solid #1d2530;border-radius:14px;padding:14px;box-shadow:0 12px 40px rgba(0,0,0,.5);font:14px system-ui;display:none}"
    + ".alf-pop.on{display:block}.alf-pop textarea{width:100%;box-sizing:border-box;background:#0b0e13;color:#e8eef2;border:1px solid #2a3340;border-radius:8px;padding:9px;font:inherit;min-height:84px}"
    + ".alf-row{display:flex;gap:8px;justify-content:flex-end;margin-top:10px}.alf-row button{font:inherit;border-radius:8px;padding:7px 12px;border:1px solid #2a3340;background:transparent;color:#e8eef2;cursor:pointer}.alf-row button.p{background:#2C7BE2;border-color:#2C7BE2}"
    + ".alf-ok{color:#22C55E;font-size:13px;margin-top:8px}";
  function el(tag, attrs, html) { var e = document.createElement(tag); for (var k in attrs) e.setAttribute(k, attrs[k]); if (html != null) e.innerHTML = html; return e; }

  function submit(message, page) {
    return fetch(api + "/feedback", {
      method: "POST", credentials: "include", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ message: message, page: page || location.pathname, user: user })
    }).then(function (r) { return r.json(); });
  }

  function mount() {
    var style = el("style"); style.textContent = css; document.head.appendChild(style);
    var btn = el("button", { class: "alf-btn" }, "💬 Feedback");
    var pop = el("div", { class: "alf-pop" },
      '<div style="font-weight:600;margin-bottom:8px">Send feedback</div>'
      + '<textarea placeholder="What could be better? A bug? An idea?"></textarea>'
      + '<div class="alf-row"><button class="alf-cancel">Cancel</button><button class="p alf-send">Send</button></div>'
      + '<div class="alf-ok" style="display:none">Thanks — filed as an issue ✓</div>');
    document.body.appendChild(btn); document.body.appendChild(pop);
    var ta = pop.querySelector("textarea"), ok = pop.querySelector(".alf-ok");
    function open() { pop.classList.add("on"); ta.focus(); }
    function close() { pop.classList.remove("on"); }
    btn.onclick = function () { pop.classList.contains("on") ? close() : open(); };
    pop.querySelector(".alf-cancel").onclick = close;
    pop.querySelector(".alf-send").onclick = function () {
      var v = ta.value.trim(); if (!v) return;
      submit(v).then(function () { ta.value = ""; ok.style.display = "block"; setTimeout(function () { ok.style.display = "none"; close(); }, 1400); });
    };
    window.AutopilotFeedback = { open: open, close: close, submit: submit };
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", mount); else mount();
})();
