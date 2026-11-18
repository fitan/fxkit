export default defineEventHandler(event => proxyToFxkit(event, storefrontBase(event)))
