FROM node:20-bookworm

WORKDIR /browser_server

RUN npx -y playwright@latest install --with-deps

COPY ./package.json ./package.json
COPY ./package-lock.json ./package-lock.json

RUN npm install

COPY ./tsconfig.json ./tsconfig.json
COPY ./src/ ./src/

CMD ["npm", "run", "start"]