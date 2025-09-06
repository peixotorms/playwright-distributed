FROM node:20-bookworm

WORKDIR /browser_server

RUN npx -y playwright@latest install --with-deps

# TODO: figure out version issue
RUN mv /root/.cache/ms-playwright/firefox-1490 /root/.cache/ms-playwright/firefox-1489 \
  && mv /root/.cache/ms-playwright/chromium_headless_shell-1187 /root/.cache/ms-playwright/chromium_headless_shell-1181

COPY ./package.json ./package.json
COPY ./package-lock.json ./package-lock.json

RUN npm install

COPY ./tsconfig.json ./tsconfig.json
COPY ./src/ ./src/

CMD ["npm", "run", "start"]
