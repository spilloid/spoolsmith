'use strict';

const tabs = [...document.querySelectorAll('[role="tab"]')];
function selectTab(tab) {
  tabs.forEach(item => {
    const selected = item === tab;
    item.setAttribute('aria-selected', String(selected));
    item.tabIndex = selected ? 0 : -1;
    document.getElementById(item.getAttribute('aria-controls')).hidden = !selected;
  });
}
tabs.forEach((tab, index) => {
  tab.addEventListener('click', () => selectTab(tab));
  tab.addEventListener('keydown', event => {
    let next;
    if (event.key === 'ArrowRight') next = (index + 1) % tabs.length;
    if (event.key === 'ArrowLeft') next = (index + tabs.length - 1) % tabs.length;
    if (event.key === 'Home') next = 0;
    if (event.key === 'End') next = tabs.length - 1;
    if (next === undefined) return;
    event.preventDefault();
    selectTab(tabs[next]);
    tabs[next].focus();
  });
});

document.querySelectorAll('.copy').forEach(button => {
  button.addEventListener('click', async () => {
    const code = button.parentElement.querySelector('code');
    const status = document.getElementById('copy-status');
    try {
      await navigator.clipboard.writeText(code.textContent.trim());
      button.textContent = 'Copied';
      status.textContent = 'Commands copied to clipboard.';
    } catch {
      const range = document.createRange();
      range.selectNodeContents(code);
      const selection = window.getSelection();
      selection.removeAllRanges();
      selection.addRange(range);
      button.textContent = 'Select & copy';
      status.textContent = 'Automatic copy is unavailable. Commands selected; press Control+C to copy.';
    }
    window.setTimeout(() => { button.textContent = 'Copy'; }, 2500);
  });
});
