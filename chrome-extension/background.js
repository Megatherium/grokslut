const capturedHeaderNames = new Set([
  "x-anonuserid",
  "x-challenge",
  "x-signature",
  "x-statsig-id",
  "user-agent",
  "accept-language",
]);
const capturedHeaderMaxAge = 5 * 60 * 1000;

chrome.webRequest.onBeforeSendHeaders.addListener(
  (details) => {
    const headers = {};
    for (const header of details.requestHeaders || []) {
      const name = header.name.toLowerCase();
      if (capturedHeaderNames.has(name) && header.value) {
        headers[name] = header.value;
      }
    }
    if (Object.keys(headers).length) {
      const key = details.url.startsWith("https://gemini.google.com/") ? "geminiHeaders" : "grokHeaders";
      const timestampsKey = `${key}CapturedAt`;
      const now = Date.now();
      chrome.storage.session.get([key, timestampsKey]).then((stored) => {
        const merged = {};
        const timestamps = {};
        for (const [name, value] of Object.entries(stored[key] || {})) {
          const capturedAt = (stored[timestampsKey] || {})[name] || 0;
          if (now - capturedAt <= capturedHeaderMaxAge) {
            merged[name] = value;
            timestamps[name] = capturedAt;
          }
        }
        for (const [name, value] of Object.entries(headers)) {
          merged[name] = value;
          timestamps[name] = now;
        }
        return chrome.storage.session.set({ [key]: merged, [timestampsKey]: timestamps });
      });
    }
  },
  { urls: ["https://grok.com/rest/app-chat/*", "https://gemini.google.com/_/BardChatUi/data/*"] },
  ["requestHeaders", "extraHeaders"],
);

const defaultAppURL = "http://127.0.0.1:8787";
const downloadDirKey = "downloadDir";
const queueStateKey = "downloadQueueState";
const queueDelayAlarm = "grokslutQueueDelay";
const validFormats = new Set(["markdown", "json", "raw-json", "zip"]);
const validProviders = new Set(["grok", "gemini"]);

let state = {
  paused: false,
  running: false,
  waitingUntil: 0,
  active: null,
  pending: [],
  completed: 0,
  failed: [],
  lastMessage: "",
};
let stateLoadPromise = null;
let workerRunning = false;
let activeAbort = null;
let activeCancelMode = "";

function normalizeDownloadDir(raw) {
  const value = String(raw || "").trim();
  if (!value) return "";
  if (value.split(/[\\/]/).includes("..")) {
    throw new Error("Download dir cannot contain '..'");
  }
  return value;
}

function safeLoopbackBaseURL(raw) {
  try {
    const parsed = new URL(String(raw || defaultAppURL));
    if (parsed.protocol === "http:" && (parsed.hostname === "127.0.0.1" || parsed.hostname === "localhost")) {
      return parsed.origin;
    }
  } catch {
    // Fall back to default loopback origin.
  }
  return defaultAppURL;
}

function makeQueueID() {
  if (globalThis.crypto?.randomUUID) return crypto.randomUUID();
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function ensureStateLoaded() {
  if (!stateLoadPromise) {
    stateLoadPromise = (async () => {
      const stored = await chrome.storage.local.get(queueStateKey);
      if (stored[queueStateKey]) {
        const saved = stored[queueStateKey];
        const pending = Array.isArray(saved.pending) ? saved.pending : [];
        if (saved.active) {
          // If the service worker died mid-fetch, re-queue the interrupted item on wake;
          // if the backend already finished writing it, createUniqueFile emits a -2 copy.
          pending.unshift(saved.active);
        }
        const paused = Boolean(saved.paused);
        const savedWait = Number(saved.waitingUntil) || 0;
        const keepWait = !paused && pending.length > 0 && savedWait > Date.now();
        state = {
          paused,
          running: keepWait,
          waitingUntil: keepWait ? savedWait : 0,
          active: null,
          pending,
          completed: Number(saved.completed) || 0,
          failed: Array.isArray(saved.failed) ? saved.failed : [],
          lastMessage: typeof saved.lastMessage === "string" ? saved.lastMessage : "",
        };
        if (keepWait) {
          const existing = await chrome.alarms.get(queueDelayAlarm);
          if (!existing) {
            await chrome.alarms.create(queueDelayAlarm, { when: keepWait });
          }
        }
      }
    })().catch((err) => {
      stateLoadPromise = null;
      throw err;
    });
  }
  return stateLoadPromise;
}

async function saveAndBroadcast() {
  await chrome.storage.local.set({ [queueStateKey]: state });
  chrome.runtime.sendMessage({ type: "queueState", state }).catch(() => {});
  try {
    const tabs = await chrome.tabs.query({ url: ["http://127.0.0.1/*", "http://localhost/*"] });
    for (const tab of tabs) {
      if (tab.id) {
        chrome.tabs.sendMessage(tab.id, { type: "queueState", state }).catch(() => {});
      }
    }
  } catch {
    // Ignore tab broadcast errors.
  }
}

async function processQueue() {
  await ensureStateLoaded();
  if (workerRunning || state.paused || state.pending.length === 0) return;
  if (state.waitingUntil > Date.now()) return;

  workerRunning = true;
  state.waitingUntil = 0;
  state.running = true;
  let scheduledAlarm = false;
  await saveAndBroadcast();

  try {
    while (state.pending.length > 0 && !state.paused) {
      const item = state.pending.shift();
      state.active = item;
      state.waitingUntil = 0;
      state.lastMessage = `Exporting ${item.title}…`;
      activeCancelMode = "";
      await saveAndBroadcast();

      try {
        const stored = await chrome.storage.local.get(downloadDirKey);
        const outDir = item.outDir !== undefined
          ? normalizeDownloadDir(item.outDir)
          : normalizeDownloadDir(stored[downloadDirKey] || "");
        const baseURL = safeLoopbackBaseURL(item.baseURL);

        activeAbort = new AbortController();
        const response = await fetch(`${baseURL}/api/export`, {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({
            provider: item.provider,
            ids: [item.id],
            format: item.format,
            outDir,
          }),
          signal: activeAbort.signal,
        });
        activeAbort = null;

        const payload = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(payload.error || `Exporter returned HTTP ${response.status}`);
        }
        const paths = Array.isArray(payload.paths) ? payload.paths : [];
        if (paths.length === 0) {
          throw new Error("Exporter returned no files");
        }

        state.completed += 1;
        state.lastMessage = `Saved ${item.title}`;
      } catch (err) {
        activeAbort = null;
        if (activeCancelMode === "clear") {
          state.lastMessage = "";
        } else if (activeCancelMode === "item") {
          state.lastMessage = `Cancelled ${item.title}`;
        } else {
          state.failed.push({
            queueId: item.queueId,
            id: item.id,
            title: item.title,
            error: err?.message || String(err),
          });
          state.lastMessage = `Failed: ${item.title}`;
        }
      }

      state.active = null;
      await saveAndBroadcast();

      if (state.pending.length > 0 && !state.paused) {
        // Use chrome.alarms so the SW can safely sleep through the 1-10s delay.
        const delayMs = 1000 + Math.floor(Math.random() * 9001);
        state.waitingUntil = Date.now() + delayMs;
        state.lastMessage = `Waiting ${Math.round(delayMs / 1000)}s before next chat…`;
        await chrome.alarms.create(queueDelayAlarm, { when: state.waitingUntil });
        if (state.paused || state.pending.length === 0) {
          state.waitingUntil = 0;
          await chrome.alarms.clear(queueDelayAlarm);
        } else {
          scheduledAlarm = true;
          break;
        }
      }
    }
  } finally {
    workerRunning = false;
    state.active = null;
    if (!scheduledAlarm) {
      state.waitingUntil = 0;
      state.running = false;
      if (activeCancelMode === "clear") {
        state.lastMessage = "";
      } else if (state.pending.length === 0 && !state.paused && (state.completed > 0 || state.failed.length > 0)) {
        state.lastMessage = `Finished: ${state.completed} saved${state.failed.length ? `, ${state.failed.length} failed` : ""}.`;
      } else if (state.paused && state.pending.length > 0) {
        state.lastMessage = `Paused (${state.pending.length} remaining).`;
      }
    }
    activeCancelMode = "";
    await saveAndBroadcast();
  }
}

chrome.alarms.onAlarm.addListener((alarm) => {
  if (alarm?.name !== queueDelayAlarm) return;
  void (async () => {
    await ensureStateLoaded();
    state.waitingUntil = 0;
    if (!state.paused && state.pending.length > 0) {
      await processQueue();
    } else {
      state.running = false;
      await saveAndBroadcast();
    }
  })();
});

const messageHandlers = {
  async getQueueState() {
    const stored = await chrome.storage.local.get(downloadDirKey);
    return { ok: true, state, downloadDir: stored[downloadDirKey] || "" };
  },
  async setDownloadDir(message) {
    const clean = normalizeDownloadDir(message.downloadDir || "");
    await chrome.storage.local.set({ [downloadDirKey]: clean });
    return { ok: true, downloadDir: clean };
  },
  async enqueueDownloads(message) {
    const provider = validProviders.has(message.provider) ? message.provider : "grok";
    const format = validFormats.has(message.format) ? message.format : "markdown";
    const baseURL = safeLoopbackBaseURL(message.baseURL);
    const rawItems = Array.isArray(message.items) ? message.items : [];
    if (rawItems.length === 0) {
      return { ok: false, error: "Select at least one conversation first." };
    }
    const stored = await chrome.storage.local.get(downloadDirKey);
    const outDir = normalizeDownloadDir(stored[downloadDirKey] || "");
    const existingKeys = new Set(
      [...(state.active ? [state.active] : []), ...state.pending].map(
        (item) => `${item.provider}:${item.format}:${item.outDir || ""}:${item.id}`,
      ),
    );
    let added = 0;
    for (const raw of rawItems) {
      const id = typeof raw?.id === "string" ? raw.id.trim() : "";
      if (!id) continue;
      const key = `${provider}:${format}:${outDir}:${id}`;
      if (existingKeys.has(key)) continue;
      existingKeys.add(key);
      const title = typeof raw?.title === "string" && raw.title.trim() ? raw.title.trim() : id;
      state.pending.push({ queueId: makeQueueID(), id, title, provider, format, outDir, baseURL });
      added += 1;
    }
    if (added > 0) {
      state.paused = false;
      state.lastMessage = `Queued ${added} chat${added === 1 ? "" : "s"}.`;
      await saveAndBroadcast();
      if (state.waitingUntil <= Date.now()) {
        state.waitingUntil = 0;
        void processQueue();
      }
    } else {
      state.lastMessage = "Selected chats are already in the queue.";
      await saveAndBroadcast();
    }
    return { ok: true, added, state };
  },
  async removeQueueItem(message) {
    const queueId = message.queueId;
    state.pending = state.pending.filter((item) => item.queueId !== queueId);
    state.failed = state.failed.filter((item) => item.queueId !== queueId);
    if (state.pending.length === 0 && !state.active && state.waitingUntil > 0) {
      state.waitingUntil = 0;
      state.running = false;
      await chrome.alarms.clear(queueDelayAlarm);
    }
    await saveAndBroadcast();
    return { ok: true, state };
  },
  async cancelActiveItem() {
    if (state.active) {
      activeCancelMode = "item";
      if (activeAbort) activeAbort.abort();
    }
    return { ok: true, state };
  },
  async pauseQueue() {
    state.paused = true;
    state.waitingUntil = 0;
    if (!workerRunning) {
      state.running = false;
    }
    await chrome.alarms.clear(queueDelayAlarm);
    state.lastMessage = state.pending.length ? `Paused (${state.pending.length} remaining).` : "Paused.";
    await saveAndBroadcast();
    return { ok: true, state };
  },
  async resumeQueue() {
    state.paused = false;
    state.waitingUntil = 0;
    await chrome.alarms.clear(queueDelayAlarm);
    await saveAndBroadcast();
    void processQueue();
    return { ok: true, state };
  },
  async clearQueue() {
    state.paused = false;
    state.pending = [];
    state.failed = [];
    state.completed = 0;
    state.waitingUntil = 0;
    await chrome.alarms.clear(queueDelayAlarm);
    if (state.active) {
      activeCancelMode = "clear";
      if (activeAbort) activeAbort.abort();
    } else {
      state.running = false;
      state.lastMessage = "";
    }
    await saveAndBroadcast();
    return { ok: true, state };
  },
};

chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
  const handler = message && Object.hasOwn(messageHandlers, message.type)
    ? messageHandlers[message.type]
    : null;
  if (!handler) return false;
  ensureStateLoaded()
    .then(() => handler(message))
    .then((result) => sendResponse(result))
    .catch((err) => {
      sendResponse({ ok: false, error: err?.message || String(err) });
    });
  return true;
});

ensureStateLoaded().then(() => {
  if (state.pending.length > 0 && !state.paused && state.waitingUntil === 0) {
    void processQueue();
  }
});
