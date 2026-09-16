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
      chrome.storage.session.get("grokHeaders").then(({ grokHeaders = {} }) =>
        chrome.storage.session.set({ grokHeaders: { ...grokHeaders, ...headers } }),
      );
    }
  },
  { urls: ["https://grok.com/rest/app-chat/*"] },
  ["requestHeaders", "extraHeaders"],
);
