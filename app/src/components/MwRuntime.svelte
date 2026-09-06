<script lang="ts">
  // Mounted once in BaseLayout (client:load). Installs window.MW, opens the
  // WebSocket, hosts the TxModal, the wrong-network banner and the toasts.
  import { onMount } from 'svelte';
  import TxModal from './TxModal.svelte';
  import NetworkMismatchBanner from './NetworkMismatchBanner.svelte';
  import Toasts from './Toasts.svelte';
  import { installMW } from '../lib/mw';
  import { ws } from '../lib/ws/client';

  onMount(() => {
    installMW();
    ws.connect();
    const onVis = () => { if (document.visibilityState === 'visible') ws.connect(); };
    document.addEventListener('visibilitychange', onVis);
    return () => document.removeEventListener('visibilitychange', onVis);
  });
</script>

<NetworkMismatchBanner />
<TxModal />
<Toasts />
