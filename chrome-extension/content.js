(() => {
  const isGrokslutPage = () =>
    document.title === "grokslut" &&
    Boolean(document.querySelector("#provider")) &&
    Boolean(document.querySelector("#tree"));

  if (!isGrokslutPage()) return;

  document.documentElement.dataset.grokslutExtension = "1";
  window.dispatchEvent(new CustomEvent("grokslut:extension-ready"));

  function collectMarkedChats() {
    const provider = document.querySelector("#provider")?.value || "grok";
    const format = document.querySelector("#format")?.value || "markdown";
    const items = [];
    document.querySelectorAll("#tree .chat").forEach((row) => {
      const box = row.querySelector('input[type="checkbox"]');
      if (!box || !box.checked) return;
      const id = row.dataset.id;
      if (!id) return;
      const title = row.querySelector("label")?.textContent || id;
      items.push({ id, title });
    });
    return {
      provider,
      format,
      baseURL: window.location.origin,
      items,
    };
  }

  window.addEventListener("message", (event) => {
    if (event.source !== window || event.origin !== window.location.origin) return;
    const data = event.data;
    if (!data || typeof data.type !== "string") return;

    if (data.type === "grokslut:enqueue") {
      chrome.runtime.sendMessage(
        {
          type: "enqueueDownloads",
          provider: data.provider,
          format: data.format,
          baseURL: window.location.origin,
          items: Array.isArray(data.items) ? data.items : [],
        },
        (response) => {
          const err = chrome.runtime.lastError?.message || response?.error || "";
          window.postMessage(
            {
              type: "grokslut:enqueue-result",
              requestId: data.requestId,
              ok: Boolean(response?.ok && !err),
              error: err,
              state: response?.state || null,
            },
            window.location.origin,
          );
        },
      );
    } else if (data.type === "grokslut:get-queue-state") {
      chrome.runtime.sendMessage({ type: "getQueueState" }, (response) => {
        if (chrome.runtime.lastError || !response?.ok) return;
        window.postMessage(
          { type: "grokslut:queue-state", state: response.state },
          window.location.origin,
        );
      });
    }
  });

  chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
    if (!message || typeof message.type !== "string") return;
    if (message.type === "getMarkedChats") {
      sendResponse({ ok: true, ...collectMarkedChats() });
      return;
    }
    if (message.type === "queueState") {
      window.postMessage(
        { type: "grokslut:queue-state", state: message.state },
        window.location.origin,
      );
    }
  });
})();
