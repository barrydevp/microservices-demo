const transco = require('../transco')

const TRANS_URL = 'http://localhost:8000'

setImmediate(async () => {
  const cli = new transco.Client(TRANS_URL)
  const session = await cli.StartSession()
  // const session = cli.SessionFromId('06fd1f26-e02a-4dd8-931f-966cf3bad462')

  const testCases = [
    {
      join: { clientId: 'c-1', requestId: '1' },
      commit: { compensate: null, compensate: null },
    },
    {
      join: { clientId: 'c-2', requestId: '2' },
      commit: { compensate: null, compensate: null },
    },
    {
      join: { clientId: 'c-3', requestId: '3' },
      commit: { compensate: null, compensate: null },
    },
  ]

  for (const test of testCases) {
    const part = await session.JoinSession(test.join)
    await part.PartialCommit(test.commit)
  }

  console.log(await session.CommitSession())
})
