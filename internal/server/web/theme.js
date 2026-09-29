// Runs before first paint so the saved theme applies without a flash.
(function () {
  try {
    var t = localStorage.getItem("gpukoll-theme");
    if (t === "light" || t === "dark") document.documentElement.dataset.theme = t;
  } catch (e) {}
})();
