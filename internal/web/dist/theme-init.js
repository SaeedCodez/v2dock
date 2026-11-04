// Apply the stored theme before first paint so there is no flash. It is a file
// rather than an inline <script> so the server's Content-Security-Policy can
// refuse every inline script.
(function () {
  try {
    // Dark is the app's default look; "system" and "light" are opt-in.
    var t = localStorage.getItem("theme") || "dark";
    var dark = t === "dark" || (t === "system" &&
      window.matchMedia("(prefers-color-scheme: dark)").matches);
    document.documentElement.classList.toggle("dark", dark);
  } catch (e) {}
})();
