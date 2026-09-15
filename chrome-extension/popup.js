const status = document.querySelector("#status");
const appURL = "http://127.0.0.1:8787";

document.querySelector("#grok").addEventListener("click", () => {
  chrome.tabs.create({ url: "https://grok.com/" });
});

document.querySelector("#app").addEventListener("click", () => {
  chrome.tabs.create({ url: appURL });
});

document.querySelector("#sync").addEventListener("click", async () => {
  status.textContent = "Reading the current Grok session…";
  try {
    const cookies = await chrome.cookies.getAll({ domain: "grok.com" });
    if (!cookies.some((cookie) => cookie.name === "sso" || cookie.name === "sso-rw")) {
      throw new Error("No signed-in Grok session found. Open Grok, sign in, and try again.");
    }
    const { grokHeaders = {} } = await chrome.storage.session.get("grokHeaders");
    const response = await fetch(`${appURL}/api/session`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        cookies: cookies.map(({ name, value, domain }) => ({ name, value, domain })),
        headers: grokHeaders,
      }),
    });
    const result = await response.json();
    if (!response.ok) throw new Error(result.error || `Exporter returned HTTP ${response.status}`);
    await chrome.storage.session.remove("grokHeaders");
    status.textContent = "Connected. Open the exporter to browse your conversations.";
  } catch (error) {
    status.textContent = error.message.includes("Failed to fetch")
      ? "The exporter is not running. Start grokslut-web, then try again."
      : error.message;
  }
});
