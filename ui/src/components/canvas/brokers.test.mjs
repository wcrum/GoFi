import assert from 'node:assert/strict'
import { test } from 'node:test'
import { hostPortProblem, jetStreamNameProblem, kafkaTopicProblem } from './sources/brokerValidation.ts'
import { brokerSummary, sourceSummary, subscribeKindOf } from '../../types.ts'

test('broker field checks mirror spec validation', () => {
  assert.equal(hostPortProblem('kafka:9092'), undefined)
  assert.equal(hostPortProblem('[::1]:9092'), undefined)
  assert.equal(hostPortProblem('kafka'), 'Use host:port.')
  assert.equal(hostPortProblem('kafka:0'), 'Port must be 1-65535.')
  assert.equal(kafkaTopicProblem('orders.v1'), undefined)
  assert.ok(kafkaTopicProblem('a b'))
  assert.equal(jetStreamNameProblem('', false), undefined)
  assert.equal(jetStreamNameProblem('', true), 'Required.')
  assert.ok(jetStreamNameProblem('a.b', false))
})

test('broker sources have a kind and a summary, even with lists omitted on the wire', () => {
  assert.equal(subscribeKindOf({ kafka: { brokers: ['k:9092'], topics: ['orders'] } }), 'kafka')
  assert.equal(subscribeKindOf({ redis: { url: 'redis://r', stream: 's' } }), 'redis')
  assert.equal(brokerSummary({ kafka: {} }), 'Kafka —')
  assert.equal(sourceSummary({ subscribe: { amqp: { url: 'amqp://mq', exchange: 'events', routingKey: 'a.#' } } }), 'AMQP events a.#')
  assert.equal(sourceSummary({ subscribe: { redis: { url: 'redis://r', channels: ['x', 'y'] } } }), 'Redis x, y')
  assert.equal(brokerSummary({ nats: { url: 'nats://n', subject: 's' } }), undefined)
})
