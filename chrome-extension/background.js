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
