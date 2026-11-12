'use strict';

(() => {
  const bridge = window.go?.main?.ReaderWindow;
  let closing = false, launchGeneration = 0, appTheme = 'dark';
  const errorText = error => typeof error === 'string' ? error : (error?.message || 'The reader window could not open.');
  function showError(error) {
    let message = document.getElementById('reader-window-error');
    if (!message) {
      message = document.createElement('p');
      message.id = 'reader-window-error';
      message.setAttribute('role', 'alert');
      document.body.append(message);
    }
    message.textContent = errorText(error);
  }
  function setTheme(theme) {
    appTheme = ['dark', 'light', 'system'].includes(theme) ? theme : 'dark';
    document.documentElement.dataset.theme = appTheme === 'system'
      ? (matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark') : appTheme;
  }
  async function openLaunch(launch) {
    if (closing) return;
    if (typeof launch?.path !== 'string' || !launch.path) throw new Error('No book was supplied to this reader window.');
    setTheme(launch.theme);
    await window.SpineGuardReader.open(launch.path, Number.isInteger(launch.issueIndex) ? launch.issueIndex : -1);
  }
  async function closeWindow() {
    if (closing) return;
    closing = true;
    ++launchGeneration;
    try {
      await window.SpineGuardReader.close();
      await bridge.CloseWindow();
    } catch (error) {
      closing = false;
      showError(error);
    }
  }
  // Native chrome, the reader's X button, and Escape share one close path.
  window.SpineGuardCloseReaderWindow = closeWindow;
  window.runtime?.EventsOn?.('reader:open', launch => {
    ++launchGeneration;
    void openLaunch(launch).catch(showError);
  });
  window.runtime?.EventsOn?.('reader:before-close', () => { void closeWindow(); });
  matchMedia('(prefers-color-scheme: light)').addEventListener('change', () => { if (appTheme === 'system') setTheme(appTheme); });
  void (async () => {
    try {
      if (!bridge?.GetReaderLaunch || !window.SpineGuardReader) throw new Error('The desktop reader connection is unavailable.');
      const request = ++launchGeneration;
      const launch = await bridge.GetReaderLaunch();
      if (request === launchGeneration && !closing) await openLaunch(launch);
    } catch (error) { showError(error); }
  })();
})();
