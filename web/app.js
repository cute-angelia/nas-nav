/* Vue 3 · local runtime, zero CDN, zero npm required at runtime */
(function () {
  'use strict';
  const { createApp } = Vue;
  let saveQueue = Promise.resolve();
  let pointerDrag = null;
  let toastTimer = null;
  const clone = v => JSON.parse(JSON.stringify(v));
  const uid = () => 'id-' + (crypto.randomUUID ? crypto.randomUUID() : Date.now() + '-' + Math.random().toString(36).slice(2));

  async function request(path, method = 'GET', body) {
    let res;
    const headers = {};
    if (body !== undefined) {
      headers['Content-Type'] = 'application/json';
      headers['X-Nav-Request'] = '1';
    } else if (!['GET', 'HEAD'].includes(method)) {
      headers['X-Nav-Request'] = '1';
    }
    try {
      res = await fetch(path, {
        method,
        credentials: 'same-origin',
        cache: 'no-store',
        headers,
        body: body === undefined ? undefined : JSON.stringify(body)
      });
    } catch (_) { throw new Error('连接失败，请检查 NAS 是否在线'); }
    let result = {};
    try { result = await res.json(); } catch (_) { /* ignore */ }
    if (!res.ok) {
      const error = new Error(result.error || '请求失败');
      error.status = res.status;
      throw error;
    }
    return result;
  }

  function normalizedUrl(text) {
    const s = (text || '').trim();
    if (!s) throw new Error('请输入网址');
    if (/^https?:\/\//i.test(s)) return s;
    if (/^[a-z][a-z0-9+.-]*:\/\//i.test(s)) throw new Error('只允许 http 或 https 地址');
    if (/^(localhost|127\.0\.0\.1|192\.168\.|10\.|172\.(1[6-9]|2\d|3[01])\.)/i.test(s)) return 'http://' + s;
    return 'https://' + s;
  }

  // Duplicate rule: hostname + effective port + pathname. Ignore search/hash.
  // Effective ports let https://a/ and https://a:443/ count as the same URL.
  function addressKey(raw) {
    const parsed = new URL(normalizedUrl(raw));
    if (!['http:', 'https:'].includes(parsed.protocol) || !parsed.hostname || parsed.username || parsed.password) {
      throw new Error('网址无效');
    }
    const port = parsed.port || (parsed.protocol === 'https:' ? '443' : '80');
    if (+port < 1 || +port > 65535) throw new Error('端口无效');
    return [parsed.hostname.toLowerCase().replace(/\.$/, ''), port, parsed.pathname || '/'].join('\u0000');
  }

  createApp({
    data() {
      return {
        loading: true, loggedIn: false, editing: false, busy: false,
        password: '', error: '', modalError: '',
        categories: [], backgroundImage: '', revision: '', search: '',
        faviconErrors: {},
        modal: '', form: { id: '', catId: '', name: '', url: '', icon: '', description: '' },
        batchForm: { targetCatId: '', newCatName: '', rawText: '', items: [] },
        toast: ''
      };
    },
    computed: {
      filteredCategories() {
        const q = this.search.trim().toLowerCase();
        if (!q) return this.categories;
        return this.categories.map(cat => ({
          ...cat,
          links: cat.links.filter(link => [link.name, link.url, link.description || ''].some(s => s.toLowerCase().includes(q)))
        })).filter(cat => cat.links.length || cat.name.toLowerCase().includes(q));
      },
      totalLinks() {
        return this.categories.reduce((sum, cat) => sum + cat.links.length, 0);
      },
      batchValidCount() {
        return (this.batchForm && this.batchForm.items ? this.batchForm.items.filter(i => !i.isDuplicate).length : 0);
      },
      batchDuplicateCount() {
        return (this.batchForm && this.batchForm.items ? this.batchForm.items.filter(i => i.isDuplicate).length : 0);
      },
      urlDuplicate() {
        if (this.modal !== 'link' || !this.form.url.trim()) return null;
        let currentKey;
        try { currentKey = addressKey(this.form.url); } catch (_) { return null; }
        for (const cat of this.categories) {
          for (const link of cat.links) {
            if (link.id === this.form.id) continue;
            try {
              if (addressKey(link.url) === currentKey) return { category: cat.name, name: link.name };
            } catch (_) { /* Ignore malformed legacy records; the server checks saves. */ }
          }
        }
        return null;
      },
      modalTitle() {
        if (this.modal === 'category') return this.form.id ? '编辑分类' : '新建分类';
        if (this.modal === 'link') return this.form.id ? '编辑网址' : '添加网址';
        if (this.modal === 'batch') return '批量粘贴导入书签';
        return '';
      }
    },
    async mounted() {
      document.addEventListener('keydown', this.onKey);
      try {
        const s = await request('/api/me');
        this.loggedIn = s.loggedIn;
        if (s.loggedIn) await this.loadData();
      } catch (e) { this.notify(e.message); }
      this.loading = false;
    },
    unmounted() { document.removeEventListener('keydown', this.onKey); this.clearPointer(); },
    methods: {
      async loadData() {
        const d = await request('/api/data');
        this.categories = d.categories || [];
        this.backgroundImage = d.backgroundImage || '';
        this.revision = d.revision;
        this.faviconErrors = {};
      },
      notify(message) {
        this.toast = message;
        clearTimeout(toastTimer);
        toastTimer = setTimeout(() => { this.toast = ''; }, 3300);
      },
      faviconUrl(raw) {
        try { return new URL('/favicon.ico', raw).href; }
        catch (_) { return ''; }
      },
      async login() {
        this.error = ''; this.busy = true;
        try {
          await request('/api/login', 'POST', { password: this.password });
          this.password = '';
          this.loggedIn = true;
          this.editing = false;
          await this.loadData();
        } catch (e) { this.error = e.message; }
        finally { this.busy = false; }
      },
      async lockPage() {
        this.clearPointer();
        try { await request('/api/logout', 'POST', {}); } catch (_) { /* session is cleared in UI */ }
        this.loggedIn = false; this.editing = false; this.password = '';
        this.search = ''; this.categories = []; this.backgroundImage = ''; this.revision = '';
      },
      async toggleEditing() {
        if (this.editing) {
          this.clearPointer();
          try {
            await saveQueue;
            this.editing = false;
            this.notify('编辑已完成');
          } catch (e) { this.notify(e.message); }
        } else {
          this.editing = true;
        }
      },
      closeModal() { if (this.busy) return; this.modal = ''; this.modalError = ''; },
      focusModal() { this.$nextTick(() => { if (this.$refs.modalInput) this.$refs.modalInput.focus(); }); },
      openCategoryForm(cat) {
        if (!this.editing) return;
        this.modal = 'category'; this.modalError = '';
        this.form = { id: cat ? cat.id : '', catId: '', name: cat ? cat.name : '', url: '', icon: '', description: '' };
        this.focusModal();
      },
      openLinkForm(catId, link) {
        if (!this.editing) return;
        this.modal = 'link'; this.modalError = '';
        this.form = { id: link ? link.id : '', catId, name: link ? link.name : '', url: link ? link.url : '', icon: link ? link.icon : '', description: link ? (link.description || '') : '' };
        this.focusModal();
      },
      openBatchModal() {
        if (!this.editing) return;
        this.modal = 'batch';
        this.modalError = '';
        this.batchForm = {
          targetCatId: this.categories.length ? this.categories[0].id : '__new__',
          newCatName: '',
          rawText: '',
          items: []
        };
        this.$nextTick(() => {
          if (this.$refs.batchTextarea) this.$refs.batchTextarea.focus();
        });
      },
      getDefaultCategoryName() {
        if (this.batchForm.targetCatId === '__new__') {
          return this.batchForm.newCatName.trim() || '新建分类';
        }
        const found = this.categories.find(c => c.id === this.batchForm.targetCatId);
        return found ? found.name : '常用网站';
      },
      handleBatchPaste(e) {
        let html = '';
        let text = '';
        if (e.clipboardData) {
          html = e.clipboardData.getData('text/html') || '';
          text = e.clipboardData.getData('text/plain') || '';
        }
        setTimeout(() => {
          const raw = this.batchForm.rawText || text;
          this.parsePastedContent(html, raw);
        }, 20);
      },
      onBatchTextInput() {
        this.parsePastedContent('', this.batchForm.rawText);
      },
      onBatchCategoryChange() {
        this.parsePastedContent('', this.batchForm.rawText);
      },
      parsePastedContent(html, text) {
        const defaultCatName = this.getDefaultCategoryName();
        const existingKeys = new Set();
        for (const cat of this.categories) {
          for (const l of cat.links) {
            try { existingKeys.add(addressKey(l.url)); } catch (_) {}
          }
        }

        const rawList = [];

        // 1. 尝试 HTML 结构解析（Chrome 书签管理器多选复制带有完整的 A 标签和目录）
        if (html && /<a\s+[^>]*href=/i.test(html)) {
          try {
            const parser = new DOMParser();
            const doc = parser.parseFromString(html, 'text/html');
            let currentFolder = defaultCatName;
            const elements = doc.querySelectorAll('h1, h2, h3, h4, h5, h6, dt, a');
            elements.forEach(el => {
              const tag = el.tagName.toUpperCase();
              if (['H1', 'H2', 'H3', 'H4', 'H5', 'H6'].includes(tag)) {
                const t = el.textContent.trim();
                if (t) currentFolder = t;
              } else if (tag === 'A') {
                const href = el.getAttribute('href') || el.href;
                const name = el.textContent.trim();
                if (href && /^https?:\/\//i.test(href)) {
                  rawList.push({ catName: currentFolder, name: name || href, url: href });
                }
              }
            });
          } catch (_) {}
        }

        // 2. 如果 HTML 未识别出任何链接，从纯文本逐行解析
        if (!rawList.length && text) {
          const lines = text.split(/\r?\n/);
          let currentFolder = defaultCatName;
          for (let line of lines) {
            line = line.trim();
            if (!line) continue;
            const catMatch = line.match(/^#+\s*(.+)$/) || line.match(/^【(.+?)】$/) || line.match(/^📁?\s*([^:：]+)[:：]$/);
            if (catMatch && !/https?:\/\//i.test(line)) {
              currentFolder = catMatch[1].trim();
              continue;
            }
            const mdMatch = line.match(/\[(.*?)\]\((https?:\/\/[^\s\)]+)\)/i);
            if (mdMatch) {
              rawList.push({ catName: currentFolder, name: mdMatch[1].trim() || mdMatch[2], url: mdMatch[2] });
              continue;
            }
            const urlMatch = line.match(/(https?:\/\/[^\s]+)/i);
            if (urlMatch) {
              const u = urlMatch[1];
              let n = line.replace(u, '').trim();
              n = n.replace(/^[-*•\d\.\s、]+/, '').trim();
              if (!n) {
                try { n = new URL(u).hostname; } catch (_) { n = u; }
              }
              rawList.push({ catName: currentFolder, name: n, url: u });
            }
          }
        }

        // 3. 规范化并标记重复
        const seenBatchKeys = new Set();
        const items = [];
        for (const item of rawList) {
          let validUrl = '';
          let key = '';
          try {
            validUrl = normalizedUrl(item.url);
            key = addressKey(validUrl);
            validUrl = new URL(validUrl).href;
          } catch (_) {
            continue;
          }

          const isDuplicate = existingKeys.has(key) || seenBatchKeys.has(key);
          if (!isDuplicate) {
            seenBatchKeys.add(key);
          }
          items.push({
            categoryName: item.catName || defaultCatName,
            name: (item.name || validUrl).slice(0, 60),
            url: validUrl,
            isDuplicate
          });
        }

        this.batchForm.items = items;
      },
      async submitModal() {
        if (this.busy) return;
        this.modalError = '';
        if (this.modal === 'batch') {
          if (this.batchForm.targetCatId === '__new__' && !this.batchForm.newCatName.trim()) {
            const hasDetectedCat = this.batchForm.items.some(i => i.categoryName && i.categoryName !== '新建分类');
            if (!hasDetectedCat) {
              this.modalError = '请输入新分类名称';
              return;
            }
          }
          const validItems = this.batchForm.items.filter(i => !i.isDuplicate);
          if (!validItems.length) {
            this.modalError = '没有可导入的新网站（网址已全部存在或未识别到有效网址）';
            return;
          }

          let addedCount = 0;
          for (const item of validItems) {
            const catName = item.categoryName.trim() || this.getDefaultCategoryName();
            let targetCat = this.categories.find(c => c.name.toLowerCase() === catName.toLowerCase());
            if (!targetCat) {
              targetCat = { id: uid(), name: catName, links: [] };
              this.categories.push(targetCat);
            }
            targetCat.links.push({
              id: uid(),
              name: item.name,
              url: item.url,
              icon: '',
              description: ''
            });
            addedCount++;
          }

          const dupCount = this.batchDuplicateCount;
          this.modal = '';
          await this.persist();
          this.notify(`已成功导入 ${addedCount} 个网站${dupCount ? `，跳过 ${dupCount} 个重复项` : ''}`);
          return;
        }
        const form = clone(this.form);
        if (!form.name.trim()) { this.modalError = '请输入名称'; return; }
        if (this.modal === 'category') {
          if (form.id) {
            const cat = this.categories.find(c => c.id === form.id);
            if (cat) cat.name = form.name.trim();
          } else {
            this.categories.push({ id: uid(), name: form.name.trim(), links: [] });
          }
        }
        if (this.modal === 'link') {
          let url;
          try {
            url = normalizedUrl(form.url);
            addressKey(url); // Includes a valid port check.
            url = new URL(url).href;
          } catch (_) { this.modalError = '网址无效，请填写 http 或 https 地址（端口需为 1–65535）'; return; }
          if (this.urlDuplicate) {
            this.modalError = `网址已存在：${this.urlDuplicate.category} / ${this.urlDuplicate.name}（域名、端口和路径重复）`;
            return;
          }
          if (Array.from(form.icon).length > 8) { this.modalError = '图标最多 8 个字符'; return; }
          const cat = this.categories.find(c => c.id === form.catId);
          if (!cat) { this.modalError = '分类不存在'; return; }
          const value = { id: form.id || uid(), name: form.name.trim(), url, icon: form.icon.trim(), description: form.description || '' };
          if (form.id) {
            const index = cat.links.findIndex(l => l.id === form.id);
            if (index !== -1) cat.links.splice(index, 1, value);
          } else cat.links.push(value);
        }
        this.modal = '';
        await this.persist();
      },
      async deleteItem() {
        const form = clone(this.form), type = this.modal;
        const name = form.name;
        if (type === 'category') {
          this.categories = this.categories.filter(c => c.id !== form.id);
        } else {
          const cat = this.categories.find(c => c.id === form.catId);
          if (cat) cat.links = cat.links.filter(l => l.id !== form.id);
        }
        this.modal = '';
        await this.persist();
        this.notify(type === 'category' ? `已删除分类「${name}」` : `已删除网址「${name}」`);
      },
      persist() {
        const snapshot = clone(this.categories);
        const backgroundImage = this.backgroundImage;
        const task = saveQueue.then(async () => {
          const result = await request('/api/data', 'PUT', { revision: this.revision, backgroundImage, categories: snapshot });
          this.revision = result.revision;
        });
        saveQueue = task.catch(() => {});
        return task.catch(async (e) => {
          if (e.status === 401) { this.loggedIn = false; this.editing = false; }
          if (e.status === 409) {
            await saveQueue;
            try { await this.loadData(); } catch (_) { /* keep previous state */ }
          }
          this.notify(e.message);
        });
      },
      async uploadBackground(e) {
        const file = e.target.files && e.target.files[0];
        e.target.value = '';
        if (!file) return;
        if (!/^image\/(jpeg|png|gif|webp)$/.test(file.type)) { this.notify('只支持 JPG、PNG、GIF 或 WebP 图片'); return; }
        if (file.size > 5 * 1024 * 1024) { this.notify('背景图片最大 5MB'); return; }
        const body = new FormData();
        body.append('background', file);
        this.busy = true;
        try {
          const res = await fetch('/api/background', {
            method: 'POST',
            credentials: 'same-origin',
            cache: 'no-store',
            headers: { 'X-Nav-Request': '1' },
            body
          });
          const result = await res.json().catch(() => ({}));
          if (!res.ok) throw new Error(result.error || '背景上传失败');
          this.backgroundImage = result.backgroundImage || '';
          this.revision = result.revision || this.revision;
          this.notify('背景已更新');
        } catch (err) { this.notify(err.message); }
        finally { this.busy = false; }
      },
      async clearBackground() {
        if (!this.backgroundImage) return;
        this.busy = true;
        try {
          const result = await request('/api/background', 'DELETE');
          this.backgroundImage = '';
          this.revision = result.revision || this.revision;
          this.notify('背景已清除');
        } catch (err) { this.notify(err.message); }
        finally { this.busy = false; }
      },
      async exportBackup() {
        if (this.busy) return;
        this.busy = true;
        try {
          const res = await fetch('/api/backup', { credentials: 'same-origin', cache: 'no-store' });
          if (!res.ok) {
            const result = await res.json().catch(() => ({}));
            const error = new Error(result.error || '导出失败');
            error.status = res.status;
            throw error;
          }
          const disposition = res.headers.get('Content-Disposition') || '';
          const match = disposition.match(/filename="([^"]+)"/i);
          const filename = match ? match[1] : 'nas-nav-backup.zip';
          const url = URL.createObjectURL(await res.blob());
          const link = document.createElement('a');
          link.href = url; link.download = filename;
          document.body.appendChild(link); link.click(); link.remove();
          URL.revokeObjectURL(url);
          this.notify('备份已导出');
        } catch (error) {
          if (error.status === 401) { this.loggedIn = false; this.editing = false; }
          this.notify(error.message);
        } finally { this.busy = false; }
      },
      async importBackup(event) {
        const file = event.target.files && event.target.files[0];
        event.target.value = '';
        if (!file || this.busy) return;
        if (file.size > 8 * 1024 * 1024) { this.notify('备份文件最大 8MB'); return; }
        this.busy = true;
        try {
          await saveQueue;
          const body = new FormData();
          body.append('backup', file);
          body.append('revision', this.revision);
          const res = await fetch('/api/backup', {
            method: 'POST', credentials: 'same-origin', cache: 'no-store',
            headers: { 'X-Nav-Request': '1' }, body
          });
          const result = await res.json().catch(() => ({}));
          if (!res.ok) {
            const error = new Error(result.error || '导入失败');
            error.status = res.status;
            throw error;
          }
          this.categories = result.categories || [];
          this.backgroundImage = result.backgroundImage || '';
          this.revision = result.revision;
          this.faviconErrors = {};
          this.notify('备份已导入');
        } catch (error) {
          if (error.status === 401) { this.loggedIn = false; this.editing = false; }
          if (error.status === 409) {
            try { await this.loadData(); } catch (_) { /* keep current state */ }
          }
          this.notify(error.message);
        } finally { this.busy = false; }
      },
      onKey(e) {
        const tag = document.activeElement && document.activeElement.tagName;
        if (e.key === 'Escape' && this.modal) { this.closeModal(); return; }
        if (e.key === '/' && this.loggedIn && !this.modal && !['INPUT', 'TEXTAREA'].includes(tag)) {
          e.preventDefault(); if (this.$refs.search) this.$refs.search.focus();
        }
      },
      startPointer(e, type, catId, linkId = null) {
        if (!this.editing || this.search || (e.pointerType === 'mouse' && e.button !== 0)) return;
        e.preventDefault(); e.stopPropagation();
        this.clearPointer();
        const el = type === 'link' ? e.currentTarget.closest('.link-tile') : e.currentTarget.closest('.section-title');
        if (!el) return;
        const rect = el.getBoundingClientRect();
        pointerDrag = { type, catId, linkId, startX: e.clientX, startY: e.clientY, rect,
          label: type === 'category' ? this.categories.find(c => c.id === catId)?.name : '',
          ghost: null, target: null, highlight: null, moved: false };
        document.addEventListener('pointermove', this.movePointer, { passive: false });
        document.addEventListener('pointerup', this.endPointer);
        document.addEventListener('pointercancel', this.cancelPointer);
      },
      movePointer(e) {
        const d = pointerDrag;
        if (!d) return;
        if (!d.moved && Math.hypot(e.clientX - d.startX, e.clientY - d.startY) < 6) return;
        if (!d.moved) {
          d.moved = true;
          document.body.classList.add('dragging');
          const ghost = document.createElement('div');
          ghost.className = 'drag-ghost';
          ghost.textContent = d.type === 'category' ? d.label : this.findLink(d.catId, d.linkId)?.name || '网址';
          ghost.style.width = Math.min(185, d.rect.width) + 'px';
          document.body.appendChild(ghost);
          d.ghost = ghost;
        }
        e.preventDefault();
        d.ghost.style.left = e.clientX + 14 + 'px';
        d.ghost.style.top = e.clientY - 16 + 'px';
        const hit = document.elementFromPoint(e.clientX, e.clientY);
        if (d.highlight) { d.highlight.classList.remove('drop-before','drop-after','drop-section'); d.highlight = null; }
        d.target = null;
        if (hit) {
          if (d.type === 'link') {
            const tile = hit.closest('.link-tile[data-link-id]');
            if (tile) {
              const r = tile.getBoundingClientRect();
              const after = e.clientX > r.left + r.width / 2;
              d.target = { catId: tile.dataset.catId, linkId: tile.dataset.linkId, after };
              d.highlight = tile;
              tile.classList.add(after ? 'drop-after' : 'drop-before');
            } else {
              const grid = hit.closest('.link-grid[data-cat-id]');
              if (grid) {
                d.target = { catId: grid.dataset.catId, linkId: null, after: true };
                d.highlight = grid; grid.classList.add('drop-section');
              }
            }
          } else {
            const section = hit.closest('.section[data-category-id]');
            if (section) {
              const h = section.querySelector('.section-title').getBoundingClientRect();
              const after = e.clientY > h.top + h.height / 2;
              d.target = {catId: section.dataset.categoryId, after};
              d.highlight = section;
              section.classList.add(after ? 'drop-after' : 'drop-before');
            }
          }
        }
        if (e.clientY < 55) window.scrollBy(0, -12);
        if (e.clientY > window.innerHeight - 55) window.scrollBy(0, 12);
      },
      async endPointer() {
        const d = pointerDrag;
        if (!d) return;
        const target = d.target;
        this.clearPointer();
        if (!d.moved || !target) return;
        let changed = false;
        if (d.type === 'link') changed = this.moveLink(d.catId, d.linkId, target);
        else changed = this.moveCategory(d.catId, target);
        if (changed) await this.persist();
      },
      cancelPointer() { this.clearPointer(); },
      clearPointer() {
        document.removeEventListener('pointermove', this.movePointer);
        document.removeEventListener('pointerup', this.endPointer);
        document.removeEventListener('pointercancel', this.cancelPointer);
        if (pointerDrag) {
          if (pointerDrag.ghost) pointerDrag.ghost.remove();
          if (pointerDrag.highlight) pointerDrag.highlight.classList.remove('drop-before','drop-after','drop-section');
        }
        pointerDrag = null;
        document.body.classList.remove('dragging');
      },
      findLink(catId, linkId) {
        const cat = this.categories.find(c => c.id === catId);
        return cat && cat.links.find(l => l.id === linkId);
      },
      moveLink(fromCatId, linkId, target) {
        const source = this.categories.find(c => c.id === fromCatId);
        const dest = this.categories.find(c => c.id === target.catId);
        if (!source || !dest) return false;
        const from = source.links.findIndex(l => l.id === linkId);
        if (from < 0 || (source === dest && linkId === target.linkId)) return false;
        const [link] = source.links.splice(from, 1);
        if (!target.linkId) dest.links.push(link);
        else {
          const index = dest.links.findIndex(l => l.id === target.linkId);
          dest.links.splice(index < 0 ? dest.links.length : index + (target.after ? 1 : 0), 0, link);
        }
        return true;
      },
      moveCategory(catId, target) {
        if (catId === target.catId) return false;
        const from = this.categories.findIndex(c => c.id === catId);
        if (from < 0) return false;
        const [cat] = this.categories.splice(from, 1);
        const to = this.categories.findIndex(c => c.id === target.catId);
        this.categories.splice(to < 0 ? this.categories.length : to + (target.after ? 1 : 0), 0, cat);
        return true;
      }
    }
  }).mount('#app');
})();
