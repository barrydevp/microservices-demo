/*
 * Copyright 2018 Google LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     https://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

'use strict';


if (process.env.DISABLE_PROFILER) {
  console.log("Profiler disabled.")
}
else {
  console.log("Profiler enabled.")
  require('@google-cloud/profiler').start({
    serviceContext: {
      service: 'paymentservice',
      version: '1.0.0'
    }
  });
}


if (process.env.DISABLE_TRACING) {
  console.log("Tracing disabled.")
}
else {
  console.log("Tracing enabled.")
  require('@google-cloud/trace-agent').start();

}

if (process.env.DISABLE_DEBUGGER) {
  console.log("Debugger disabled.")
}
else {
  console.log("Debugger enabled.")
  require('@google-cloud/debug-agent').start({
    serviceContext: {
      service: 'paymentservice',
      version: 'VERSION'
    }
  });
}


const express = require('express')
const http = require('http')
const path = require('path');
const HipsterShopServer = require('./server');

const PORT = process.env['PORT'];
const PROTO_PATH = path.join(__dirname, '/proto/');

const server = new HipsterShopServer(PROTO_PATH, PORT);

const app = express()

// all environments
app.set('port', process.env.HTTP_PORT || 8181)
app.use(express.logger('dev'))
app.use(express.methodOverride())
// app.use(express.session({ secret: 'your secret here' }))
app.use(express.bodyParser())
// app.use(app.router)
// app.use(express.static(path.join(__dirname, 'public')))

app.post('/compensate', (req, res) => {
  console.log('====> COMPENSATE')
  console.log(req.body)
  console.log('=========')

  res.send({ payment_compensate_ok: 1 })
})

http.createServer(app).listen(app.get('port'), () => {
  console.log('HTTP server listening on port ' + app.get('port'))
})

server.listen();



