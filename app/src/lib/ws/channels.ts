// Channel names understood by backend/internal/ws/subscriptions.go.
export const tokenChannel = (collection: string, tokenId: string | bigint) => `token:${collection.toLowerCase()}:${tokenId}`;
export const collectionChannel = (collection: string) => `collection:${collection.toLowerCase()}`;
export const userChannel = (address: string) => `user:${address.toLowerCase()}`;
export const txChannel = (hash: string) => `tx:${hash.toLowerCase()}`;
/** Everything (activity feed, listings/auctions index pages). */
export const ACTIVITY_CHANNEL = 'activity';
