const adobeAnalyticsScript = 'https://assets.adobedtm.com/b968b9f97b30/3cd471a5f9f8/launch-97c2cab3681f.min.js';
const mcpActivityAnalyticsScript = 'https://markets.jpmorgan.com/jpmcp-cm/content/dam/jpm-cp/markets-client-portal/public/script/mcpActivityAnalytics.js'
const externalScripts = [adobeAnalyticsScript, mcpActivityAnalyticsScript];

externalScripts.forEach(externalScript => {
  const script = document.createElement('script');
  script.type = 'text/javascript';
  script.src = externalScript;
  script.async = true;
  document.head.appendChild(script); 
});