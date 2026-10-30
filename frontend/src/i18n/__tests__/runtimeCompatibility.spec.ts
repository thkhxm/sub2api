import { afterEach, describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'
import { getLocale, i18n, loadLocaleMessages } from '../index'

const initialLocale = i18n.global.locale.value

afterEach(() => {
  i18n.global.locale.value = initialLocale
})

describe('多语言运行时兼容与安全转义', () => {
  it('加载真实中英文目录后可切换管理密钥文案', async () => {
    await loadLocaleMessages('zh')
    i18n.global.locale.value = 'zh'
    expect(getLocale()).toBe('zh')
    expect(i18n.global.t('adminAccessKeys.title')).toBe('管理员访问密钥')
    expect(i18n.global.t('adminAccessKeys.summary', { globals: 2, rules: 3 })).toBe('全站权限：2 项 · 资源规则：3 条')

    await loadLocaleMessages('en')
    i18n.global.locale.value = 'en'
    expect(getLocale()).toBe('en')
    expect(i18n.global.t('adminAccessKeys.title')).toBe('Admin access keys')
  })

  it('保留命名插值和数量复数规则', () => {
    const instance = createI18n({
      legacy: false,
      locale: 'en',
      messages: { en: { quota: '{used} / {limit} GiB', nodes: 'no nodes | one node | {count} nodes' } }
    })
    expect(instance.global.t('quota', { used: 12, limit: 40 })).toBe('12 / 40 GiB')
    expect(instance.global.t('nodes', 0)).toBe('no nodes')
    expect(instance.global.t('nodes', 1)).toBe('one node')
    expect(instance.global.t('nodes', 3)).toBe('3 nodes')
    instance.dispose()
  })

  it('HTML 参数转义后不能留下可执行的 iframe srcdoc 属性', () => {
    const instance = createI18n({
      legacy: false,
      locale: 'en',
      escapeParameter: true,
      warnHtmlMessage: false,
      messages: { en: { frame: '<iframe srcdoc="{content}"></iframe>' } }
    })
    // jsdom 不执行脚本，仅检查浏览器解析后的属性，避免只比对字符串而漏掉实体解码。
    const container = document.createElement('div')
    container.innerHTML = instance.global.t('frame', { content: '<script>window.fixtureExecuted = true</script>' })
    const iframe = container.querySelector('iframe')
    expect(iframe).not.toBeNull()
    expect(iframe?.hasAttribute('srcdoc')).toBe(false)
    expect(container.querySelector('script')).toBeNull()
    instance.dispose()
  })
})
