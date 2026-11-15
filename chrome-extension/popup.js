const status = document.querySelector("#status");
const downloadDirInput = document.querySelector("#download-dir");
const dirStatus = document.querySelector("#dir-status");
const queueSummary = document.querySelector("#queue-summary");
const queueList = document.querySelector("#queue-list");
const togglePauseBtn = document.querySelector("#toggle-pause");
const clearQueueBtn = document.querySelector("#clear-queue");
const appURL = "http://127.0.0.1:8787";

let currentState = null;

document.querySelector("#grok").addEventListener("click", () => {
  chrome.tabs.create({ url: "https://grok.com/" });
});

document.querySelector("#gemini").addEventListener("click", () => {
  chrome.tabs.create({ url: "https://gemini.google.com/" });
});

document.querySelector("#app").addEventListener("click", () => {
  chrome.tabs.create({ url: appURL });
});

async function sync(provider) {
  const isGemini = provider === "gemini";
  const label = isGemini ? "Gemini" : "Grok";
  status.textContent = `Reading the current ${label} session…`;
  try {
    const cookies = await chrome.cookies.getAll({ url: isGemini ? "https://gemini.google.com/" : "https://grok.com/" });
    const signedIn = isGemini
      ? cookies.some((cookie) => cookie.name === "__Secure-1PSID" || cookie.name === "__Secure-3PSID")
      : cookies.some((cookie) => cookie.name === "sso" || cookie.name === "sso-rw");
    if (!signedIn) {
      throw new Error(`No signed-in ${label} session found. Open ${label}, sign in, and try again.`);
    }
    const headerKey = isGemini ? "geminiHeaders" : "grokHeaders";
    const stored = await chrome.storage.session.get(headerKey);
    const response = await fetch(`${appURL}/api/session/${provider}`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        cookies: cookies.map(({ name, value, domain }) => ({ name, value, domain })),
        headers: stored[headerKey] || {},
      }),
    });
    const result = await response.json();
    if (!response.ok) throw new Error(result.error || `Exporter returned HTTP ${response.status}`);
    await chrome.storage.session.remove([headerKey, `${headerKey}CapturedAt`]);
    status.textContent = `${label} connected. Open the exporter to browse your conversations.`;
  } catch (error) {
    status.textContent = error.message.includes("Failed to fetch")
      ? "The exporter is not running. Start grokslut-web, then try again."
      : error.message;
  }
}

document.querySelector("#sync-grok").addEventListener("click", () => sync("grok"));
document.querySelector("#sync-gemini").addEventListener("click", () => sync("gemini"));

function renderDirStatus(dir) {
  dirStatus.textContent = dir
    ? `Saving directly to: ${dir}`
    : "Saving to default exporter directory (exports/)";
}

function makeQueueRow(item, kind) {
  const row = document.createElement("div");
  row.className = `q-item ${kind}`;

  const meta = document.createElement("div");
  meta.className = "q-meta";

  const title = document.createElement("div");
  title.className = "q-title";
  title.textContent = item.title || item.id;

  const sub = document.createElement("div");
  sub.className = "q-sub";
  if (kind === "active") {
    sub.textContent = `Saving • ${item.provider} • ${item.format}`;
  } else if (kind === "failed") {
    sub.textContent = `Failed: ${item.error || "unknown error"}`;
  } else {
    sub.textContent = `Queued • ${item.provider} • ${item.format}`;
  }

  meta.append(title, sub);
  row.append(meta);

  const btn = document.createElement("button");
  btn.className = "q-del";
  btn.type = "button";
  if (kind === "active") {
    btn.textContent = "Cancel";
    btn.title = "Cancel current download";
    btn.addEventListener("click", async () => {
      const resp = await chrome.runtime.sendMessage({ type: "cancelActiveItem" });
      if (resp?.state) renderQueue(resp.state);
    });
  } else {
    btn.textContent = "×";
    btn.title = "Remove from queue";
    btn.addEventListener("click", async () => {
      const resp = await chrome.runtime.sendMessage({ type: "removeQueueItem", queueId: item.queueId });
      if (resp?.state) renderQueue(resp.state);
    });
  }
  row.append(btn);
  return row;
}

function renderQueue(state) {
  if (!state) return;
  currentState = state;
  const pendingCount = state.pending?.length || 0;
  const failedCount = state.failed?.length || 0;
  const hasWork = Boolean(state.active) || pendingCount > 0;

  togglePauseBtn.disabled = !hasWork;
  togglePauseBtn.textContent = state.paused ? "Resume" : "Pause";
  clearQueueBtn.disabled = !hasWork && failedCount === 0 && (state.completed || 0) === 0;

  const parts = [];
  if (state.active) parts.push("1 active");
  if (pendingCount) parts.push(`${pendingCount} queued`);
  if (state.completed) parts.push(`${state.completed} done`);
  if (failedCount) parts.push(`${failedCount} failed`);
  const counts = parts.length ? parts.join(" • ") : "Queue empty";
  queueSummary.textContent = state.lastMessage ? `${counts} — ${state.lastMessage}` : counts;

  queueList.replaceChildren();
  if (state.active) {
    queueList.append(makeQueueRow(state.active, "active"));
  }
  for (const item of state.pending || []) {
    queueList.append(makeQueueRow(item, "pending"));
  }
  for (const item of state.failed || []) {
    queueList.append(makeQueueRow(item, "failed"));
  }
}

document.querySelector("#save-dir").addEventListener("click", async () => {
  const resp = await chrome.runtime.sendMessage({
    type: "setDownloadDir",
    downloadDir: downloadDirInput.value,
  });
  if (!resp?.ok) {
    dirStatus.textContent = resp?.error || "Could not save download dir.";
    return;
  }
  downloadDirInput.value = resp.downloadDir;
  renderDirStatus(resp.downloadDir);
});

document.querySelector("#queue-downloads").addEventListener("click", async () => {
  try {
    const tabs = await chrome.tabs.query({ url: ["http://127.0.0.1/*", "http://localhost/*"] });
    const activeFirst = [...tabs].sort((a, b) => Number(b.active) - Number(a.active));
    let selection = null;
    for (const tab of activeFirst) {
      if (!tab.id) continue;
      try {
        const reply = await chrome.tabs.sendMessage(tab.id, { type: "getMarkedChats" });
        if (reply?.ok) {
          selection = reply;
          break;
        }
      } catch {
        // Tab is not a grokslut page or needs refresh.
      }
    }
    if (!selection) {
      queueSummary.textContent = "Open the grokslut exporter tab (and refresh it if just reloaded), mark chats, and try again.";
      return;
    }
    if (!selection.items?.length) {
      queueSummary.textContent = "No chats marked in the exporter tab.";
      return;
    }
    const resp = await chrome.runtime.sendMessage({
      type: "enqueueDownloads",
      provider: selection.provider,
      format: selection.format,
      baseURL: selection.baseURL,
      items: selection.items,
    });
    if (!resp?.ok) {
      queueSummary.textContent = resp?.error || "Failed to queue downloads.";
      return;
    }
    renderQueue(resp.state);
  } catch (error) {
    queueSummary.textContent = error.message;
  }
});

togglePauseBtn.addEventListener("click", async () => {
  const type = currentState?.paused ? "resumeQueue" : "pauseQueue";
  const resp = await chrome.runtime.sendMessage({ type });
  if (resp?.state) renderQueue(resp.state);
});

clearQueueBtn.addEventListener("click", async () => {
  const resp = await chrome.runtime.sendMessage({ type: "clearQueue" });
  if (resp?.state) renderQueue(resp.state);
});

chrome.runtime.onMessage.addListener((message) => {
  if (message?.type === "queueState" && message.state) {
    renderQueue(message.state);
  }
});

chrome.runtime.sendMessage({ type: "getQueueState" }).then((resp) => {
  if (!resp?.ok) return;
  downloadDirInput.value = resp.downloadDir || "";
  renderDirStatus(resp.downloadDir || "");
  renderQueue(resp.state);
});
