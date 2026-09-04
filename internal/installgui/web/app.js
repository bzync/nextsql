(function () {
  "use strict";

  var STEPS = ["Welcome", "Location", "Resources", "Administrator", "Summary", "Install"];

  var state = {
    step: 0,
    hello: null,
    params: {
      dataDir: "",
      keyFile: "",
      preset: "balanced",
      bufferPages: 0,
      adminUser: "",
      adminPassword: "",
      realm: "default",
      database: "default",
    },
    lastPlan: null,
    planError: null,
    installResult: null,
    installError: null,
    installing: false,
  };

  function tokenFromCookie() {
    var m = document.cookie.match(/(?:^|; )nsi_token=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : "";
  }

  function api(path, body) {
    return fetch(path, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Installer-Token": tokenFromCookie(),
      },
      body: JSON.stringify(body),
    }).then(function (r) {
      return r.json().then(function (j) {
        if (!r.ok) throw new Error(j.error || ("request failed: " + r.status));
        return j;
      });
    });
  }

  function getJSON(path) {
    return fetch(path, { headers: { "X-Installer-Token": tokenFromCookie() } }).then(function (r) {
      return r.json().then(function (j) {
        if (!r.ok) throw new Error(j.error || ("request failed: " + r.status));
        return j;
      });
    });
  }

  function h(tag, attrs) {
    var el = document.createElement(tag);
    attrs = attrs || {};
    for (var k in attrs) {
      if (k === "text") el.textContent = attrs[k];
      else if (k === "html") el.innerHTML = attrs[k];
      else if (k.indexOf("on") === 0 && typeof attrs[k] === "function") el.addEventListener(k.slice(2), attrs[k]);
      else if (typeof attrs[k] === "boolean") { if (attrs[k]) el.setAttribute(k, ""); }
      else el.setAttribute(k, attrs[k]);
    }
    for (var i = 2; i < arguments.length; i++) {
      var child = arguments[i];
      if (child == null) continue;
      el.appendChild(typeof child === "string" ? document.createTextNode(child) : child);
    }
    return el;
  }

  function stepsBar(current) {
    var bar = h("div", { class: "steps" });
    STEPS.forEach(function (name, i) {
      var attrs = {};
      if (i === current) attrs["aria-current"] = "step";
      bar.appendChild(h("span", attrs, name));
    });
    return bar;
  }

  function render() {
    var app = document.getElementById("app");
    app.innerHTML = "";
    var view;
    switch (state.step) {
      case 0: view = viewWelcome(); break;
      case 1: view = viewLocation(); break;
      case 2: view = viewResources(); break;
      case 3: view = viewAdmin(); break;
      case 4: view = viewSummary(); break;
      case 5: view = viewInstall(); break;
      default: view = viewDone();
    }
    app.appendChild(view);
  }

  function card(children) {
    var c = h("div", { class: "card" });
    children.forEach(function (ch) { if (ch) c.appendChild(ch); });
    return c;
  }

  function passwordStrength(pw) {
    if (!pw) return { label: "", ok: false };
    var classes = 0;
    if (/[a-z]/.test(pw)) classes++;
    if (/[A-Z]/.test(pw)) classes++;
    if (/[0-9]/.test(pw)) classes++;
    if (/[^A-Za-z0-9]/.test(pw)) classes++;
    if (pw.length < 8) return { label: "Too short (minimum 8 characters)", ok: false };
    if (pw.length >= 14 && classes >= 3) return { label: "Strong", ok: true };
    if (pw.length >= 10 && classes >= 2) return { label: "Good", ok: true };
    return { label: "Weak — consider a longer password with mixed character types", ok: true };
  }

  // ---- Step 0: Welcome -----------------------------------------------

  function viewWelcome() {
    var v = state.hello;
    var info = v
      ? h("dl", { class: "summary" },
          h("dt", {}, "Version"), h("dd", {}, v.nextsql_version + " (phase " + v.phase + ")"),
          h("dt", {}, "Platform"), h("dd", {}, v.defaults.os + (v.defaults.elevated ? " (elevated)" : "")))
      : h("p", { class: "hint" }, "Detecting…");
    return card([
      stepsBar(0),
      h("h1", {}, "NextSQL Setup"),
      h("p", { class: "lede" }, "This wizard creates a new, encrypted-by-default NextSQL database on this machine. Nothing is written to disk until you confirm the summary screen."),
      info,
      h("div", { class: "actions" },
        h("span"),
        h("button", { class: "primary", onclick: function () { state.step = 1; render(); } }, "Get started")),
    ]);
  }

  // ---- Step 1: Location -------------------------------------------------

  function viewLocation() {
    var p = state.params;
    var planBanner = null;
    if (state.planError) {
      planBanner = h("div", { class: "banner error" }, state.planError);
    } else if (state.lastPlan && state.lastPlan.result) {
      var r = state.lastPlan.result;
      planBanner = h("div", { class: "banner ok" });
      if (r.hardware) {
        planBanner.appendChild(h("dl", { class: "summary" },
          h("dt", {}, "Disk free"), h("dd", {}, humanBytes(r.hardware.disk_free_bytes) + " of " + humanBytes(r.hardware.disk_total_bytes))));
      }
      if (r.warnings && r.warnings.length) {
        var ul = h("ul", { class: "warnings" });
        r.warnings.forEach(function (w) { ul.appendChild(h("li", {}, w)); });
        planBanner.appendChild(ul);
      }
    }

    function onCheck() {
      checkPlan().then(render);
    }

    return card([
      stepsBar(1),
      h("h2", {}, "Data directory & unlock key"),
      h("p", { class: "hint" }, "The data directory holds the encrypted database. The key file unlocks it and must never leave this machine — keep it off the data volume in production."),
      h("label", { for: "dataDir" }, "Data directory"),
      h("input", {
        type: "text", id: "dataDir", value: p.dataDir,
        oninput: function (e) { p.dataDir = e.target.value; },
      }),
      h("label", { for: "keyFile" }, "Root unlock key file"),
      h("input", {
        type: "text", id: "keyFile", value: p.keyFile,
        oninput: function (e) { p.keyFile = e.target.value; },
      }),
      h("p", { class: "hint" }, "Both are created if missing. Existing data or keys at these paths are never deleted or overwritten by this wizard."),
      planBanner,
      h("div", { class: "actions" },
        h("button", { onclick: function () { state.step = 0; render(); } }, "Back"),
        h("span", {},
          h("button", { onclick: onCheck }, "Check"),
          " ",
          h("button", { class: "primary", onclick: function () { state.step = 2; render(); onCheck(); } }, "Continue"))),
    ]);
  }

  // ---- Step 2: Resources -------------------------------------------------

  function viewResources() {
    var p = state.params;
    var presets = [
      ["conservative", "Conservative", "~10% of RAM for the buffer pool — leaves headroom for other services on this machine."],
      ["balanced", "Balanced (recommended)", "~25% of RAM — a reasonable default for a dedicated or lightly shared machine."],
      ["high-performance", "High performance", "~50% of RAM — for a machine dedicated to NextSQL."],
      ["custom", "Custom", "Set the buffer pool size yourself, in pages."],
    ];
    var fieldset = h("fieldset", {});
    presets.forEach(function (row) {
      var id = "preset-" + row[0];
      var input = h("input", {
        type: "radio", name: "preset", id: id, value: row[0],
        onchange: function () { p.preset = row[0]; checkPlan().then(render); },
      });
      if (p.preset === row[0]) input.checked = true;
      var wrap = h("div", { class: "radio-row" }, input, h("label", { for: id }, row[1]));
      fieldset.appendChild(wrap);
      fieldset.appendChild(h("p", { class: "hint" }, row[2]));
    });

    var customInput = null;
    if (p.preset === "custom") {
      customInput = h("div", {},
        h("label", { for: "bufferPages" }, "Buffer pool pages"),
        h("input", {
          type: "number", id: "bufferPages", min: "16", value: String(p.bufferPages || ""),
          oninput: function (e) { p.bufferPages = parseInt(e.target.value, 10) || 0; },
        }));
    }

    var rec = null;
    if (state.lastPlan && state.lastPlan.result && state.lastPlan.result.recommendation) {
      var r = state.lastPlan.result.recommendation;
      rec = h("div", { class: "banner ok" }, "Buffer pool: " + r.buffer_pages + " pages (" + humanBytes(r.buffer_bytes) + ") — " + r.rationale);
    }

    return card([
      stepsBar(2),
      h("h2", {}, "Resource preset"),
      fieldset,
      customInput,
      rec,
      h("div", { class: "actions" },
        h("button", { onclick: function () { state.step = 1; render(); } }, "Back"),
        h("button", { class: "primary", onclick: function () { state.step = 3; render(); } }, "Continue")),
    ]);
  }

  // ---- Step 3: Administrator ----------------------------------------------

  function viewAdmin() {
    var p = state.params;
    var confirmVal = viewAdmin._confirm || "";
    var strength = passwordStrength(p.adminPassword);
    var mismatch = confirmVal !== "" && confirmVal !== p.adminPassword;

    return card([
      stepsBar(3),
      h("h2", {}, "Administrator account"),
      h("p", { class: "hint" }, "Optional, but recommended: this account can connect and administer the new database immediately."),
      h("label", { for: "adminUser" }, "Username"),
      h("input", { type: "text", id: "adminUser", value: p.adminUser, oninput: function (e) { p.adminUser = e.target.value; } }),
      h("label", { for: "adminPassword" }, "Password"),
      h("input", {
        type: "password", id: "adminPassword", value: p.adminPassword,
        oninput: function (e) { p.adminPassword = e.target.value; render(); },
      }),
      p.adminPassword ? h("p", { class: "hint" }, strength.label) : null,
      h("label", { for: "adminPasswordConfirm" }, "Confirm password"),
      h("input", {
        type: "password", id: "adminPasswordConfirm", value: confirmVal,
        oninput: function (e) { viewAdmin._confirm = e.target.value; render(); },
      }),
      mismatch ? h("div", { class: "banner error" }, "Passwords do not match.") : null,
      h("p", { class: "hint" }, "This password is sent once, over this local connection, straight into a private file the installer reads and deletes — it is never written to a URL, a log, or uploaded anywhere."),
      h("div", { class: "actions" },
        h("button", { onclick: function () { state.step = 2; render(); } }, "Back"),
        h("button", {
          class: "primary",
          disabled: (p.adminPassword || confirmVal) && (mismatch || p.adminPassword.length < 8),
          onclick: function () {
            if ((p.adminUser || p.adminPassword) && mismatch) return;
            state.step = 4;
            render();
          },
        }, "Continue")),
    ]);
  }

  // ---- Step 4: Summary ----------------------------------------------------

  function viewSummary() {
    var p = state.params;
    return card([
      stepsBar(4),
      h("h2", {}, "Review"),
      h("p", { class: "lede" }, "Nothing has been created yet. Installing will:"),
      h("dl", { class: "summary" },
        h("dt", {}, "Data directory"), h("dd", {}, p.dataDir),
        h("dt", {}, "Unlock key file"), h("dd", {}, p.keyFile),
        h("dt", {}, "Resource preset"), h("dd", {}, p.preset + (p.preset === "custom" ? " (" + p.bufferPages + " pages)" : "")),
        h("dt", {}, "Administrator"), h("dd", {}, p.adminUser || "(none — add one later with `nextsql init`’s user tools)"),
        h("dt", {}, "Realm / database"), h("dd", {}, p.realm + " / " + p.database)),
      h("div", { class: "banner warn" }, "Write down the unlock key file path above. It is required every time the server starts and is never uploaded or recoverable by NextSQL if lost."),
      h("div", { class: "actions" },
        h("button", { onclick: function () { state.step = 3; render(); } }, "Back"),
        h("button", { class: "primary", onclick: function () { state.step = 5; render(); doInstall(); } }, "Install")),
    ]);
  }

  // ---- Step 5: Install / progress -----------------------------------------

  function viewInstall() {
    if (state.installing) {
      return card([
        stepsBar(5),
        h("h2", {}, "Installing…"),
        h("p", {}, h("span", { class: "spinner" }), "Creating the database and verifying it. This usually takes a few seconds."),
      ]);
    }
    return viewDone();
  }

  function viewDone() {
    if (state.installError) {
      return card([
        h("h2", {}, "Setup failed"),
        h("div", { class: "banner error" }, state.installError),
        h("div", { class: "actions" },
          h("button", { onclick: function () { state.step = 4; render(); } }, "Back to summary"),
          h("span")),
      ]);
    }
    var r = state.installResult;
    var health = r && r.health;
    return card([
      h("h2", {}, "NextSQL is ready"),
      health
        ? h("div", { class: health.ok ? "banner ok" : "banner error" }, health.ok ? "Health check passed." : "Health check reported a problem — see the server logs.")
        : null,
      h("p", {}, "Start the server with:"),
      h("pre", {}, "nextsqld --data-dir " + state.params.dataDir + " \\\n  --key-file " + state.params.keyFile + " \\\n  --listen 127.0.0.1:7210"),
      h("p", { class: "hint" }, "You can close this tab now."),
    ]);
  }

  // ---- data calls ---------------------------------------------------------

  function checkPlan() {
    return api("/api/v1/plan", state.params).then(function (res) {
      state.lastPlan = res;
      state.planError = res.ok ? null : (res.error || "unknown error");
    }, function (err) {
      state.planError = err.message;
    });
  }

  function doInstall() {
    state.installing = true;
    state.installError = null;
    api("/api/v1/install", state.params).then(function (res) {
      state.installing = false;
      if (res.ok) {
        state.installResult = res.result;
      } else {
        state.installError = res.error || "unknown error";
      }
      render();
    }, function (err) {
      state.installing = false;
      state.installError = err.message;
      render();
    });
  }

  function humanBytes(n) {
    if (!n) return "0 B";
    var units = ["B", "KiB", "MiB", "GiB", "TiB"];
    var i = 0;
    while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
    return n.toFixed(i === 0 ? 0 : 1) + " " + units[i];
  }

  // ---- boot -----------------------------------------------------------

  getJSON("/api/v1/hello").then(function (res) {
    state.hello = res;
    if (res.defaults) {
      state.params.dataDir = res.defaults.dataDir;
      state.params.keyFile = res.defaults.keyFile;
    }
    render();
  }, function (err) {
    document.getElementById("app").textContent = "Failed to reach the installer: " + err.message;
  });
})();
