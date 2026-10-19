/* Apply the colour scheme before the first paint so the app never flashes the
   wrong theme. Telegram's colourScheme (when present) takes over right after
   the SDK loads.

   This lives in its own file (instead of an inline <script>) so the page can
   be served with a strict Content-Security-Policy that allows scripts from
   the app itself and from Telegram only. */
(function () {
  try {
    var dark = window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches;
    document.documentElement.dataset.scheme = dark ? "dark" : "light";
  } catch (e) {
    document.documentElement.dataset.scheme = "light";
  }
})();
