import { test, expect } from './fixtures';

test('saves the engine URL', async ({ appConfigPage, page }) => {
  const url = page.getByRole('textbox', { name: /engine url/i });
  await url.clear();
  await url.fill('http://argus-engine.argus.svc:8080');

  const saveResponse = appConfigPage.waitForSettingsResponse();
  await page.getByRole('button', { name: /save engine settings/i }).click();
  await expect(saveResponse).toBeOK();
});
