import { getCaptchaToken } from "../src/proxy/captcha.js";

const appVersion = process.argv[2] || "3.11.2";
try {
  const token = await getCaptchaToken(appVersion);
  process.stdout.write(JSON.stringify(token));
  process.exit(0);
} catch (err) {
  process.stderr.write(String(err));
  process.exit(1);
}
