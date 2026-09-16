const capturedHeaderNames = new Set([
  "x-anonuserid",
  "x-challenge",
  "x-signature",
  "x-statsig-id",
  "user-agent",
  "accept-language",
]);

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
      chrome.storage.session.get(key).then((stored) =>
        chrome.storage.session.set({ [key]: { ...(stored[key] || {}), ...headers } }),
      );
    }
  },
  { urls: ["https://grok.com/rest/app-chat/*", "https://gemini.google.com/_/BardChatUi/data/*"] },
  ["requestHeaders", "extraHeaders"],
);
