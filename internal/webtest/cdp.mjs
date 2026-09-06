// Minimal, dependency-free Chrome DevTools Protocol harness used by the two
// P28 browser accessibility checks. Chrome itself is intentionally external;
// axe-core remains a package-local development dependency in each frontend.
import { spawn } from "node:child_process";
import { accessSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function chromeBinary() {
  const candidates = [process.env.CHROME_BIN, "/usr/bin/google-chrome", "/usr/bin/chromium", "/usr/bin/chromium-browser"].filter(Boolean);
  for (const candidate of candidates) {
    try {
      accessSync(candidate);
      return candidate;
    } catch {
      // Try the next known executable.
    }
  }
  throw new Error("Chrome/Chromium not found (set CHROME_BIN to run the browser audit)");
}

export async function launchChrome(url, { width = 1280, height = 900 } = {}) {
  const profile = mkdtempSync(join(tmpdir(), "nextsql-ui-a11y-"));
  const stderr = [];
  const child = spawn(chromeBinary(), [
    "--headless=new",
    "--no-sandbox",
    "--disable-gpu",
    "--disable-background-networking",
    "--disable-component-update",
    "--disable-default-apps",
    "--disable-extensions",
    "--disable-sync",
    "--metrics-recording-only",
    "--no-first-run",
    "--no-default-browser-check",
    "--remote-debugging-port=0",
    `--user-data-dir=${profile}`,
    `--window-size=${width},${height}`,
    url,
  ], { stdio: ["ignore", "ignore", "pipe"] });
  child.stderr.on("data", (chunk) => {
    if (stderr.join("").length < 8000) stderr.push(String(chunk));
  });

  let port;
  const portFile = join(profile, "DevToolsActivePort");
  const deadline = Date.now() + 15_000;
  while (Date.now() < deadline) {
    try {
      port = Number(readFileSync(portFile, "utf8").split("\n")[0]);
      if (port) break;
    } catch {
      // Chrome has not opened the debugging endpoint yet.
    }
    if (child.exitCode !== null) throw new Error(`Chrome exited early (${child.exitCode}): ${stderr.join("")}`);
    await delay(50);
  }
  if (!port) throw new Error(`Chrome debugging endpoint did not start: ${stderr.join("")}`);

  let target;
  while (Date.now() < deadline) {
    const targets = await fetch(`http://127.0.0.1:${port}/json/list`).then((response) => response.json());
    target = targets.find((item) => item.type === "page");
    if (target?.webSocketDebuggerUrl) break;
    await delay(50);
  }
  if (!target?.webSocketDebuggerUrl) throw new Error("Chrome page target did not appear");

  const socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    socket.addEventListener("open", resolve, { once: true });
    socket.addEventListener("error", reject, { once: true });
  });
  let nextID = 1;
  const pending = new Map();
  socket.addEventListener("message", (event) => {
    const message = JSON.parse(String(event.data));
    if (!message.id || !pending.has(message.id)) return;
    const { resolve, reject } = pending.get(message.id);
    pending.delete(message.id);
    if (message.error) reject(new Error(`${message.error.message}: ${JSON.stringify(message.error.data ?? {})}`));
    else resolve(message.result);
  });

  function send(method, params = {}) {
    const id = nextID++;
    return new Promise((resolve, reject) => {
      pending.set(id, { resolve, reject });
      socket.send(JSON.stringify({ id, method, params }));
    });
  }

  async function evaluate(expression) {
    const result = await send("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true, userGesture: true });
    if (result.exceptionDetails) {
      throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text || "browser evaluation failed");
    }
    return result.result.value;
  }

  async function waitFor(expression, message = expression, timeout = 8_000) {
    const until = Date.now() + timeout;
    while (Date.now() < until) {
      if (await evaluate(`Boolean(${expression})`)) return;
      await delay(40);
    }
    throw new Error(`Timed out waiting for ${message}`);
  }

  async function press(key) {
    const keys = {
      Enter: { code: "Enter", virtual: 13, text: "\r" },
      Escape: { code: "Escape", virtual: 27 },
      ArrowUp: { code: "ArrowUp", virtual: 38 },
      ArrowDown: { code: "ArrowDown", virtual: 40 },
      Home: { code: "Home", virtual: 36 },
      End: { code: "End", virtual: 35 },
      Tab: { code: "Tab", virtual: 9 },
    };
    const current = keys[key] ?? { code: key, virtual: 0 };
    const params = {
      key,
      code: current.code,
      windowsVirtualKeyCode: current.virtual,
      nativeVirtualKeyCode: current.virtual,
      ...(current.text ? { text: current.text, unmodifiedText: current.text } : {}),
    };
    await send("Input.dispatchKeyEvent", { type: "keyDown", ...params });
    await send("Input.dispatchKeyEvent", { type: "keyUp", ...params, text: undefined, unmodifiedText: undefined });
  }

  await send("Runtime.enable");
  await send("Page.enable");
  await waitFor("document.readyState === 'complete'", "the page to load");

  async function reload() {
    await send("Page.reload", {});
    await waitFor("document.readyState === 'complete'", "the page to reload");
  }

  return {
    evaluate,
    waitFor,
    press,
    reload,
    insertText: (text) => send("Input.insertText", { text }),
    emulateMedia: (features) => send("Emulation.setEmulatedMedia", { features }),
    emulateDeviceMetrics: ({ width, height, deviceScaleFactor = 1, mobile = false }) => send("Emulation.setDeviceMetricsOverride", {
      width,
      height,
      deviceScaleFactor,
      mobile,
    }),
    clearDeviceMetrics: () => send("Emulation.clearDeviceMetricsOverride"),
    async close() {
      socket.close();
      child.kill("SIGTERM");
      await Promise.race([
        new Promise((resolve) => child.once("exit", resolve)),
        delay(2_000).then(() => child.kill("SIGKILL")),
      ]);
      rmSync(profile, { recursive: true, force: true });
    },
  };
}

export async function runAxe(browser, axeSource, label) {
  // Do not sample a component halfway through its short entrance fade: the
  // temporary parent opacity changes effective contrast even though the
  // settled UI is compliant. Motion preference behavior is asserted by CSS
  // and MotionConfig; axe evaluates the stable view.
  await browser.evaluate("new Promise((resolve) => setTimeout(resolve, 250))");
  const result = await browser.evaluate(`(async () => {
    if (!window.axe) { ${axeSource} }
    const result = await window.axe.run(document, {
      runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"] }
    });
    return result.violations.map((violation) => ({
      id: violation.id,
      impact: violation.impact,
      help: violation.help,
      nodes: violation.nodes.map((node) => ({
        target: node.target.join(" "),
        html: node.html,
        failure: node.failureSummary
      }))
    }));
  })()`);
  if (result.length) {
    throw new Error(`${label}: axe found ${result.length} violation(s)\n${JSON.stringify(result, null, 2)}`);
  }
}
