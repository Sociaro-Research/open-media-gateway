// Generation-focused suites enter their synthetic key through the supported
// manual field. Account creation/restore has its own managed-key regression.
async function useFixtureKey(page,secret){
 await page.locator('#apiKey').fill(secret);
 await page.locator('#apiKey').press(process.env.UI_BROWSER_ENGINE==='webkit'?'Alt+Tab':'Tab');
}
module.exports={useFixtureKey};
