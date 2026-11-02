# The quickstart client. Dependencies are installed at build time rather than
# at run time, so the example does not depend on a bind mount being writable or
# on the host having a node_modules left over from something else.
FROM node:26-alpine
WORKDIR /app
COPY package.json ./
RUN npm install --no-fund --no-audit --loglevel=error
COPY query.mjs ./
CMD ["node", "query.mjs"]
