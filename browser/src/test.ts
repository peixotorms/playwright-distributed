import { chromium, type Browser } from 'playwright';

const wsEndpoint = process.env.WS_ENDPOINT;

if (!wsEndpoint) {
    console.error('ERROR: The WS_ENDPOINT environment variable is not set.');
    console.error('Please run the script like this:');
    console.error('WS_ENDPOINT="ws://127.0.0.1:4000/playwright/your-worker-id" bun run src/test.ts');
    process.exit(1);
}

Promise.all(Array.from({ length: 1 }, (_, i) => {
    console.log(`Starting test instance ${i + 1}`);
    return main();
}));

async function main() {
    let browser: Browser | null = null;
    try {
        console.log(`Connecting to browser at: ${wsEndpoint}`);

        browser = await chromium.connect(wsEndpoint!, {
            timeout: 5000,
        });

        console.log('Successfully connected to the browser!');

        const context = await browser.newContext();
        const page = await context.newPage();

        console.log('Navigating to broton.dev...');
        await page.goto('https://broton.dev');

        const title = await page.title();
        console.log(`Page title is: "${title}"`);

        await page.close();
        console.log('Page closed.');

    } catch (error) {
        console.error('An error occurred while trying to connect to the browser:', error);
        process.exit(1);
    } finally {
        if (browser) {
            await browser.close();
            console.log('Browser connection closed.');
        }
    }
}
