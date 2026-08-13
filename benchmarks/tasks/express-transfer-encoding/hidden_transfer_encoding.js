'use strict'

const assert = require('node:assert/strict')
const fs = require('node:fs')
const vm = require('node:vm')

const source = fs.readFileSync('lib/response.js', 'utf8')
const start = source.indexOf('res.send = function send(body)')
const end = source.indexOf('\n};', start)
assert.notEqual(start, -1, 'res.send implementation not found')
assert.notEqual(end, -1, 'res.send implementation terminator not found')

const context = {
  ArrayBuffer,
  Buffer,
  res: {},
  setCharset: value => value
}
vm.runInNewContext(`${source.slice(start, end + 3)}`, context, { filename: 'lib/response.js' })

function sendWith(transferEncoding) {
  const headers = new Map()
  if (transferEncoding !== undefined) {
    headers.set('transfer-encoding', transferEncoding)
  }
  const response = {
    app: { get: () => undefined },
    req: { fresh: false, method: 'GET' },
    statusCode: 200,
    end() {},
    get(name) { return headers.get(name.toLowerCase()) },
    removeHeader(name) { headers.delete(name.toLowerCase()) },
    set(name, value) { headers.set(name.toLowerCase(), value); return this },
    status(code) { this.statusCode = code; return this },
    type(value) { headers.set('content-type', value); return this }
  }
  context.res.send.call(response, '')
  return headers
}

for (const encoding of ['chunked', 'gzip', 'compress', 'custom-coding']) {
  const headers = sendWith(encoding)
  assert.equal(headers.get('transfer-encoding'), encoding)
  assert.equal(headers.has('content-length'), false, 'res.send set Content-Length with Transfer-Encoding')
}
assert.equal(sendWith(undefined).get('content-length'), 0)
console.log('Express Transfer-Encoding behavior passed')
