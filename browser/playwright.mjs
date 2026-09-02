export { chromium } from 'playwright';

export function createBrowserOptions(config = {}) {
  return {
    headless: config.headless !== false,
  };
}
