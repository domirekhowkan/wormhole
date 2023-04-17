import { expect } from "@jest/globals";
import { NodeHttpTransport } from "@improbable-eng/grpc-web-node-http-transport";
import { LCDClient, MnemonicKey, TxInfo } from "@terra-money/terra.js";
import axios from "axios";
import { ChainId, getSignedVAAWithRetry } from "../../..";
import {
  TERRA_CHAIN_ID,
  TERRA_GAS_PRICES_URL,
  TERRA_NODE_URL,
  TERRA_PRIVATE_KEY,
  WORMHOLE_RPC_HOSTS,
} from "./consts";
import { SuiTransactionBlockResponse, TransactionBlock } from "@mysten/sui.js";
import { SuiCoinObject } from "../../../sui/types";
import { hexZeroPad } from "ethers/lib/utils";

export async function waitForTerraExecution(
  transaction: string,
  lcd: LCDClient
): Promise<TxInfo | undefined> {
  let done: boolean = false;
  let info;
  while (!done) {
    await new Promise((resolve) => setTimeout(resolve, 1000));
    try {
      info = await lcd.tx.txInfo(transaction);
      if (info) {
        done = true;
      }
    } catch (e) {
      console.error(e);
    }
  }
  if (info && info.code !== 0) {
    // error code
    throw new Error(
      `Tx ${transaction}: error code ${info.code}: ${info.raw_log}`
    );
  }
  return info;
}

export async function getSignedVAABySequence(
  chainId: ChainId,
  sequence: string,
  emitterAddress: string
): Promise<Uint8Array> {
  //Note, if handed a sequence which doesn't exist or was skipped for consensus this will retry until the timeout.
  const { vaaBytes } = await getSignedVAAWithRetry(
    WORMHOLE_RPC_HOSTS,
    chainId,
    emitterAddress,
    sequence,
    {
      transport: NodeHttpTransport(), //This should only be needed when running in node.
    },
    1000, //retryTimeout
    1000 //Maximum retry attempts
  );

  return vaaBytes;
}

export async function queryBalanceOnTerra(asset: string): Promise<number> {
  const lcd = new LCDClient({
    URL: TERRA_NODE_URL,
    chainID: TERRA_CHAIN_ID,
    isClassic: true,
  });
  const mk = new MnemonicKey({
    mnemonic: TERRA_PRIVATE_KEY,
  });
  const wallet = lcd.wallet(mk);

  let balance: number = NaN;
  try {
    let coins: any;
    let pagnation: any;
    [coins, pagnation] = await lcd.bank.balance(wallet.key.accAddress);
    if (coins) {
      let coin = coins.get(asset);
      if (coin) {
        balance = parseInt(coin.toData().amount);
      } else {
        console.error(
          "failed to query coin balance, coin [" +
            asset +
            "] is not in the wallet, coins: %o",
          coins
        );
      }
    } else {
      console.error("failed to query coin balance!");
    }
  } catch (e) {
    console.error("failed to query coin balance: %o", e);
  }

  return balance;
}

export async function getTerraGasPrices() {
  return axios.get(TERRA_GAS_PRICES_URL).then((result) => result.data);
}

// https://github.com/microsoft/TypeScript/issues/34523
export const assertIsNotNull: <T>(x: T | null) => asserts x is T = (x) => {
  expect(x).not.toBeNull();
};

export const assertIsNotNullOrUndefined: <T>(
  x: T | null | undefined
) => asserts x is T = (x) => {
  expect(x).not.toBeNull();
  expect(x).not.toBeUndefined();
};

export function mintAndTransferCoinSui(
  treasuryCap: string,
  coinType: string,
  amount: bigint,
  recipient: string
) {
  const tx = new TransactionBlock();
  tx.moveCall({
    target: "0x2::coin::mint_and_transfer",
    arguments: [tx.object(treasuryCap), tx.pure(amount), tx.pure(recipient)],
    typeArguments: [coinType],
  });
  return tx;
}

export const getEmitterAddressAndSequenceFromResponseSui = (
  coreBridgePackageId: string,
  response: SuiTransactionBlockResponse
): { emitterAddress: string; sequence: string } => {
  const wormholeMessageEventType = `${coreBridgePackageId}::publish_message::WormholeMessage`;
  const event = response.events?.find(
    (e) => e.type === wormholeMessageEventType
  );
  if (event === undefined) {
    throw new Error(`${wormholeMessageEventType} event type not found`);
  }
  const { sender, sequence } = event.parsedJson || {};
  if (sender === undefined || sequence === undefined) {
    throw new Error("Unexpected response payload");
  }
  return { emitterAddress: sender.substring(2), sequence };
};
